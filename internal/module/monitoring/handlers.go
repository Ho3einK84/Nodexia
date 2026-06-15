package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/http/httperrors"
	"github.com/Ho3einK84/Nodexia/internal/http/middleware"
	"github.com/Ho3einK84/Nodexia/internal/livemetrics"
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/sshclient"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

type PageHandler struct {
	deps         module.Dependencies
	serverRepo   servers.Repository
	snapshotRepo Repository
	trafficRepo  TrafficRepository
}

type RefreshHandler struct {
	deps         module.Dependencies
	serverRepo   servers.Repository
	snapshotRepo Repository
	trafficRepo  TrafficRepository
}

type FormInput struct {
	Password         string
	PrivateKey       string
	KeyPassphrase    string
	ConnectTimeout   string
	CommandTimeout   string
	TrafficInterface string
}

type ValidationErrors map[string]string

func NewPageHandler(deps module.Dependencies, serverRepo servers.Repository, snapshotRepo Repository, trafficRepo TrafficRepository) PageHandler {
	return PageHandler{deps: deps, serverRepo: serverRepo, snapshotRepo: snapshotRepo, trafficRepo: trafficRepo}
}

func NewRefreshHandler(deps module.Dependencies, serverRepo servers.Repository, snapshotRepo Repository, trafficRepo TrafficRepository) RefreshHandler {
	return RefreshHandler{deps: deps, serverRepo: serverRepo, snapshotRepo: snapshotRepo, trafficRepo: trafficRepo}
}

func (h PageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	wantRefresh := strings.TrimSpace(r.URL.Query().Get("refresh")) == "1"
	shouldCollect := hasStoredCreds && wantRefresh

	if hasStoredCreds && !wantRefresh {
		hasStored, _ := h.snapshotRepo.HasAny(r.Context(), server.ID)
		if !hasStored {
			shouldCollect = true
		}
	}

	if shouldCollect {
		h.collectAndRender(w, r, server)
		return
	}

	form := defaultFormView(h.deps, server, hasStoredCreds)
	snapshot := view.MonitoringSnapshotView{}
	if latest, err := h.snapshotRepo.GetLatestByServer(r.Context(), server.ID); err == nil {
		snapshot = snapshotViewFromModel(latest)
	} else if !errors.Is(err, ErrNotFound) {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load monitoring snapshot", "The latest stored monitoring snapshot could not be loaded.")
		return
	}

	traffic := view.MonitoringTrafficSnapshotView{}
	if latestTraffic, err := h.trafficRepo.GetLatestTrafficByServer(r.Context(), server.ID); err == nil {
		traffic = trafficViewFromModel(latestTraffic)
		if form.TrafficInterface == "" {
			form.TrafficInterface = latestTraffic.InterfaceName
		}
	} else if !errors.Is(err, ErrNotFound) {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not load vnStat snapshot", "The latest stored vnStat snapshot could not be loaded.")
		return
	}

	renderPage(w, r, h.deps, http.StatusOK, server, form, snapshot, view.MonitoringCollectionResultView{}, traffic, view.MonitoringTrafficCollectionResultView{}, "", "")
}

func (h PageHandler) collectAndRender(w http.ResponseWriter, r *http.Request, server servers.Server) {
	password, privateKey, keyPassphrase := servers.ResolveCredentials(server)
	connectTimeout := h.deps.Config.SSH.ConnectTimeout
	commandTimeout := h.deps.Config.SSH.CommandTimeout

	connReq := sshclient.ConnectionRequest{
		Host:           server.Host,
		Port:           server.Port,
		Username:       server.Username,
		AuthMode:       server.AuthMode,
		Password:       password,
		PrivateKeyPEM:  privateKey,
		KeyPassphrase:  keyPassphrase,
		ConnectTimeout: connectTimeout,
	}

	collectCtx, cancelCollect := boundedCollectionContext(r.Context(), h.deps)
	defer cancelCollect()

	snapshot, result, err := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	})

	collectionResult := view.MonitoringCollectionResultView{
		Available:   true,
		Command:     "resource snapshot collector",
		Duration:    formatDuration(result.Duration),
		CollectedAt: formatTimestamp(result.CompletedAt),
		Stdout:      result.Stdout,
		Stderr:      result.Stderr,
	}

	if err != nil {
		collectionResult.Error = err.Error()
		form := defaultFormView(h.deps, server, true)
		form.RefreshURL = monitoringURL(server.ID)
		renderPage(w, r, h.deps, http.StatusBadGateway, server, form, view.MonitoringSnapshotView{}, collectionResult, view.MonitoringTrafficSnapshotView{}, view.MonitoringTrafficCollectionResultView{}, "error", "Resource monitoring collection failed.")
		return
	}

	snapshot.ServerID = server.ID
	stored, storeErr := h.snapshotRepo.Append(r.Context(), snapshot)
	if storeErr != nil {
		httperrors.RenderPage(w, r, h.deps, storeErr, "/servers", "Could not persist monitoring snapshot", "Monitoring data was collected but could not be stored.")
		return
	}

	// Use the previously stored interface as a hint; selectTrafficInterface will
	// fall back to the busiest interface if the hint has zero accumulated traffic.
	trafficInterface := ""
	if latestTraffic, trafficErr := h.trafficRepo.GetLatestTrafficByServer(r.Context(), server.ID); trafficErr == nil {
		trafficInterface = latestTraffic.InterfaceName
	}

	trafficSnapshot, trafficResult, trafficErr := CollectTraffic(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: connReq,
		CommandTimeout:    commandTimeout,
	}, trafficInterface)

	trafficCollection := view.MonitoringTrafficCollectionResultView{
		Available:   true,
		Command:     "vnstat --json",
		Duration:    formatDuration(trafficResult.Duration),
		CollectedAt: formatTimestamp(trafficResult.CompletedAt),
		Stdout:      trafficResult.Stdout,
		Stderr:      trafficResult.Stderr,
	}
	if trafficErr != nil {
		trafficCollection.Error = trafficErr.Error()
	} else {
		trafficSnapshot.ServerID = server.ID
		storedTraffic, appendErr := h.trafficRepo.AppendTraffic(r.Context(), trafficSnapshot)
		if appendErr != nil {
			trafficCollection.Error = "vnStat data was collected but could not be stored."
		} else {
			trafficSnapshot = storedTraffic
		}
	}

	flashKind := "success"
	flashMessage := "Resource monitoring snapshot was collected and stored successfully."
	if trafficCollection.Error != "" {
		flashKind = "error"
		flashMessage = "Resource snapshot was stored, but vnStat integration did not complete successfully."
	} else if !trafficSnapshot.Available {
		flashKind = "success"
		flashMessage = "Resource snapshot was stored. vnStat data is not available on this server yet."
	} else {
		flashMessage = "Resource and vnStat snapshots were collected and stored successfully."
	}

	hasStoredCreds := servers.HasStoredCredentials(server)
	form := defaultFormView(h.deps, server, hasStoredCreds)
	form.TrafficInterface = trafficSnapshot.InterfaceName
	form.RefreshURL = monitoringURL(server.ID)

	renderPage(
		w, r, h.deps, http.StatusOK, server, form,
		snapshotViewFromModel(stored),
		collectionResult,
		trafficViewFromModel(trafficSnapshot),
		trafficCollection,
		flashKind, flashMessage,
	)
}

func (h RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, ok := loadServer(w, r, h.deps, h.serverRepo)
	if !ok {
		return
	}

	if servers.HasStoredCredentials(server) {
		http.Redirect(w, r, monitoringURL(server.ID)+"?refresh=1", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Invalid monitoring request", "The submitted monitoring request could not be parsed.")
		return
	}

	form := formInputFromRequest(r, h.deps)
	validationErrors, connectTimeout, commandTimeout := validateForm(form, server, h.deps)
	if validationErrors.HasAny() {
		renderPage(
			w, r, h.deps, http.StatusUnprocessableEntity, server,
			formViewFromInput(form, validationErrors, server.ID),
			view.MonitoringSnapshotView{}, view.MonitoringCollectionResultView{},
			view.MonitoringTrafficSnapshotView{}, view.MonitoringTrafficCollectionResultView{},
			"error", "Please fix the highlighted fields before refreshing monitoring data.",
		)
		return
	}

	collectCtx, cancelCollect := boundedCollectionContext(r.Context(), h.deps)
	defer cancelCollect()

	snapshot, result, err := Collect(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       server.AuthMode,
			Password:       form.Password,
			PrivateKeyPEM:  form.PrivateKey,
			KeyPassphrase:  form.KeyPassphrase,
			ConnectTimeout: connectTimeout,
		},
		CommandTimeout: commandTimeout,
	})

	collectionResult := view.MonitoringCollectionResultView{
		Available:   true,
		Command:     "resource snapshot collector",
		Duration:    formatDuration(result.Duration),
		CollectedAt: formatTimestamp(result.CompletedAt),
		Stdout:      result.Stdout,
		Stderr:      result.Stderr,
	}

	if err != nil {
		collectionResult.Error = err.Error()
		renderPage(
			w, r, h.deps, http.StatusBadGateway, server,
			defaultFormView(h.deps, server, false),
			view.MonitoringSnapshotView{}, collectionResult,
			view.MonitoringTrafficSnapshotView{}, view.MonitoringTrafficCollectionResultView{},
			"error", "Resource monitoring collection failed.",
		)
		return
	}

	snapshot.ServerID = server.ID
	stored, err := h.snapshotRepo.Append(r.Context(), snapshot)
	if err != nil {
		httperrors.RenderPage(w, r, h.deps, err, "/servers", "Could not persist monitoring snapshot", "Monitoring data was collected but could not be stored.")
		return
	}

	trafficSnapshot, trafficResult, trafficErr := CollectTraffic(collectCtx, h.deps.SSH, sshclient.CommandRequest{
		ConnectionRequest: sshclient.ConnectionRequest{
			Host:           server.Host,
			Port:           server.Port,
			Username:       server.Username,
			AuthMode:       server.AuthMode,
			Password:       form.Password,
			PrivateKeyPEM:  form.PrivateKey,
			KeyPassphrase:  form.KeyPassphrase,
			ConnectTimeout: connectTimeout,
		},
		CommandTimeout: commandTimeout,
	}, form.TrafficInterface)

	trafficCollection := view.MonitoringTrafficCollectionResultView{
		Available:   true,
		Command:     "vnstat --json",
		Duration:    formatDuration(trafficResult.Duration),
		CollectedAt: formatTimestamp(trafficResult.CompletedAt),
		Stdout:      trafficResult.Stdout,
		Stderr:      trafficResult.Stderr,
	}
	if trafficErr != nil {
		trafficCollection.Error = trafficErr.Error()
	} else {
		trafficSnapshot.ServerID = server.ID
		storedTraffic, err := h.trafficRepo.AppendTraffic(r.Context(), trafficSnapshot)
		if err != nil {
			trafficCollection.Error = "vnStat data was collected but could not be stored."
		} else {
			trafficSnapshot = storedTraffic
		}
	}

	flashKind := "success"
	flashMessage := "Resource monitoring snapshot was collected and stored successfully."
	if trafficCollection.Error != "" {
		flashKind = "error"
		flashMessage = "Resource snapshot was stored, but vnStat integration did not complete successfully."
	} else if !trafficSnapshot.Available {
		flashKind = "success"
		flashMessage = "Resource snapshot was stored. vnStat data is not available on this server yet."
	} else {
		flashMessage = "Resource and vnStat snapshots were collected and stored successfully."
	}

	renderPage(
		w, r, h.deps, http.StatusOK, server,
		formViewWithTraffic(h.deps, server.ID, trafficSnapshot.InterfaceName),
		snapshotViewFromModel(stored),
		collectionResult,
		trafficViewFromModel(trafficSnapshot),
		trafficCollection,
		flashKind, flashMessage,
	)
}

// boundedCollectionContext caps synchronous SSH collection so it completes (or
// fails) comfortably within the HTTP write timeout. Without it, a slow handshake
// can outlast NODEXIA_HTTP_WRITE_TIMEOUT and the server tears the response down
// mid-render, surfacing as a 500 with "superfluous response.WriteHeader".
func boundedCollectionContext(ctx context.Context, deps module.Dependencies) (context.Context, context.CancelFunc) {
	writeTimeout := deps.Config.HTTP.WriteTimeout
	if writeTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	budget := writeTimeout - 2*time.Second
	if budget <= 0 {
		budget = writeTimeout / 2
	}
	return context.WithTimeout(ctx, budget)
}

func monitoringURL(serverID int64) string {
	return "/servers/" + formatID(serverID) + "/monitoring"
}

func loadServer(w http.ResponseWriter, r *http.Request, deps module.Dependencies, serverRepo servers.Repository) (servers.Server, bool) {
	serverID, ok := pathID(r)
	if !ok {
		httperrors.RenderPage(w, r, deps, servers.ErrNotFound, "/servers", "Server not found", "The requested server record does not exist.")
		return servers.Server{}, false
	}

	server, err := serverRepo.GetByID(r.Context(), serverID)
	if err != nil {
		httperrors.RenderPage(w, r, deps, err, "/servers", "Could not load server", "The monitoring page could not load the selected server.")
		return servers.Server{}, false
	}
	return server, true
}

func renderPage(
	w http.ResponseWriter,
	r *http.Request,
	deps module.Dependencies,
	statusCode int,
	server servers.Server,
	form view.MonitoringFormView,
	snapshot view.MonitoringSnapshotView,
	collection view.MonitoringCollectionResultView,
	traffic view.MonitoringTrafficSnapshotView,
	trafficCollection view.MonitoringTrafficCollectionResultView,
	flashKind string,
	flashMessage string,
) {
	page := view.NewPageData(deps.Config, r)
	page.CSRFToken = middleware.GetCSRFToken(r.Context())
	page.Title = page.T("monitoring.title")
	page.ActiveNav = "/servers"
	page.ContentTemplate = "content-monitoring"
	page.PageTitle = page.T("monitoring.page_title", "server", server.Name)
	page.SetServerCountry(server.CountryCode, server.CountryName)
	page.PageDescription = page.T("monitoring.page_description")
	if deps.Database != nil {
		page.MigrationCount = deps.Database.MigrationCount()
	}
	page.MonitoringTarget = view.MonitoringTargetView{
		ID:                 server.ID,
		Name:               server.Name,
		Host:               server.Host,
		Port:               server.Port,
		AuthMode:           server.AuthMode,
		Username:           server.Username,
		Tags:               server.Tags,
		CredentialStrategy: server.CredentialStrategy,
		CredentialRef:      server.CredentialRef,
		UpdatedAt:          formatTimestamp(server.UpdatedAt),
	}
	page.MonitoringForm = form
	page.MonitoringSnapshot = snapshot
	page.MonitoringCollection = collection
	page.MonitoringTraffic = traffic
	page.MonitoringTrafficCollection = trafficCollection
	page.MonitoringLive = liveView(deps, server)
	page.FlashKind = flashKind
	page.FlashMessage = flashMessage
	page.PageStyles = []string{"/static/monitoring.css"}
	page.PageScripts = []string{"/static/monitoring.js", "/static/livemetrics.js"}

	if err := deps.Renderer.Render(w, statusCode, page); err != nil {
		http.Error(w, "render monitoring page", http.StatusInternalServerError)
	}
}

// liveView assembles the real-time panel's view data. Live streaming needs both
// the hub and stored SSH credentials (resolved server-side); when either is
// missing the panel renders a note instead of opening a socket.
func liveView(deps module.Dependencies, server servers.Server) view.MonitoringLiveView {
	intervalSeconds := int(livemetrics.DefaultInterval.Seconds())
	if deps.LiveMetrics != nil {
		intervalSeconds = int(deps.LiveMetrics.Interval().Seconds())
	}
	return view.MonitoringLiveView{
		Enabled:         deps.LiveMetrics != nil && servers.HasStoredCredentials(server),
		WSURL:           liveURL(server.ID),
		IntervalSeconds: intervalSeconds,
	}
}

func defaultFormView(deps module.Dependencies, server servers.Server, hasStoredCreds bool) view.MonitoringFormView {
	return view.MonitoringFormView{
		Action:                     "/servers/" + formatID(server.ID) + "/monitoring",
		ConnectTimeout:             deps.Config.SSH.ConnectTimeout.String(),
		CommandTimeout:             deps.Config.SSH.CommandTimeout.String(),
		TrafficInterface:           "",
		StoredCredentialsAvailable: hasStoredCreds,
		RefreshURL:                 monitoringURL(server.ID),
		Errors:                     map[string]string{},
	}
}

func formViewWithTraffic(deps module.Dependencies, serverID int64, trafficInterface string) view.MonitoringFormView {
	form := defaultFormView(deps, servers.Server{ID: serverID}, false)
	form.TrafficInterface = trafficInterface
	return form
}

func formInputFromRequest(r *http.Request, deps module.Dependencies) FormInput {
	return FormInput{
		Password:         r.FormValue("password"),
		PrivateKey:       r.FormValue("private_key"),
		KeyPassphrase:    r.FormValue("key_passphrase"),
		ConnectTimeout:   fallbackString(strings.TrimSpace(r.FormValue("connect_timeout")), deps.Config.SSH.ConnectTimeout.String()),
		CommandTimeout:   fallbackString(strings.TrimSpace(r.FormValue("command_timeout")), deps.Config.SSH.CommandTimeout.String()),
		TrafficInterface: strings.TrimSpace(r.FormValue("traffic_interface")),
	}
}

func formViewFromInput(input FormInput, validationErrors ValidationErrors, serverID int64) view.MonitoringFormView {
	return view.MonitoringFormView{
		Action:           "/servers/" + formatID(serverID) + "/monitoring",
		ConnectTimeout:   input.ConnectTimeout,
		CommandTimeout:   input.CommandTimeout,
		TrafficInterface: input.TrafficInterface,
		Errors:           validationErrors,
	}
}

func validateForm(input FormInput, server servers.Server, deps module.Dependencies) (ValidationErrors, time.Duration, time.Duration) {
	validationErrors := ValidationErrors{}

	connectTimeout, err := parseDurationField(input.ConnectTimeout, deps.Config.SSH.ConnectTimeout)
	if err != nil {
		validationErrors["connect_timeout"] = "Enter a valid connection timeout such as 10s or 30s."
	}

	commandTimeout, err := parseDurationField(input.CommandTimeout, deps.Config.SSH.CommandTimeout)
	if err != nil {
		validationErrors["command_timeout"] = "Enter a valid command timeout such as 20s or 1m."
	}

	if !isSafeTrafficInterface(input.TrafficInterface) {
		validationErrors["traffic_interface"] = "Use only letters, numbers, dots, dashes, underscores, or colons for the vnStat interface."
	}

	password := strings.TrimSpace(input.Password)
	privateKey := strings.TrimSpace(input.PrivateKey)
	switch server.AuthMode {
	case "password":
		if password == "" {
			validationErrors["password"] = "Enter the SSH password for this runtime session."
		}
	case "key":
		if privateKey == "" {
			validationErrors["private_key"] = "Paste the SSH private key for this runtime session."
		}
	case "hybrid":
		if password == "" && privateKey == "" {
			validationErrors["password"] = "Provide a password or private key for hybrid authentication."
			validationErrors["private_key"] = "Provide a private key or password for hybrid authentication."
		}
	default:
		if password == "" && privateKey == "" {
			validationErrors["password"] = "Provide runtime SSH credentials before continuing."
		}
	}

	return validationErrors, connectTimeout, commandTimeout
}

func snapshotViewFromModel(snapshot Snapshot) view.MonitoringSnapshotView {
	return view.MonitoringSnapshotView{
		Available:      true,
		CPUUsage:       formatPercent(snapshot.CPUUsage),
		RAMUsage:       formatPercent(snapshot.RAMUsage),
		DiskUsage:      formatPercent(snapshot.DiskUsage),
		LoadAverage1:   formatLoad(snapshot.LoadAverage1),
		LoadAverage5:   formatLoad(snapshot.LoadAverage5),
		LoadAverage15:  formatLoad(snapshot.LoadAverage15),
		UptimeHuman:    formatUptime(snapshot.UptimeSeconds),
		NetworkSummary: fallbackDisplay(snapshot.NetworkSummary),
		CollectedAt:    formatTimestamp(snapshot.CreatedAt),
	}
}

func trafficViewFromModel(snapshot TrafficSnapshot) view.MonitoringTrafficSnapshotView {
	now := time.Now().UTC()
	todayLabel := now.Format("2006-01-02")
	yesterdayLabel := now.AddDate(0, 0, -1).Format("2006-01-02")
	currentMonthLabel := now.Format("2006-01")

	rowsDaily := buildTrafficRowViews(snapshot.DailyRows, todayLabel, yesterdayLabel)
	rowsMonthly := buildTrafficRowViews(snapshot.MonthlyRows, currentMonthLabel, "")
	reverseTrafficRowViews(rowsDaily)
	reverseTrafficRowViews(rowsMonthly)

	currentMonthRX := "-"
	if snapshot.Available {
		for _, row := range snapshot.MonthlyRows {
			if row.Label == currentMonthLabel {
				currentMonthRX = formatBytes(row.RXBytes)
				break
			}
		}
	}

	return view.MonitoringTrafficSnapshotView{
		Known:               snapshot.ID > 0 || snapshot.Message != "" || len(snapshot.AvailableInterfaces) > 0,
		Available:           snapshot.Available,
		VnstatMissing:       !snapshot.Available && strings.TrimSpace(snapshot.Message) == "vnStat is not installed on the target server.",
		InterfaceName:       fallbackDisplay(snapshot.InterfaceName),
		AvailableInterfaces: snapshot.AvailableInterfaces,
		Message:             fallbackDisplay(snapshot.Message),
		DailyRows:           rowsDaily,
		MonthlyRows:         rowsMonthly,
		PeakMbps:            formatMbps(snapshot.PeakMbps),
		AvgMbps:             formatMbps(snapshot.AvgMbps),
		CurrentMonthRX:      currentMonthRX,
		CollectedAt:         formatTimestamp(snapshot.CollectedAt),
	}
}

func buildTrafficRowViews(rows []TrafficRow, latestLabel, secondLabel string) []view.MonitoringTrafficRowView {
	maxTotal := int64(0)
	for _, row := range rows {
		if row.TotalBytes > maxTotal {
			maxTotal = row.TotalBytes
		}
	}

	result := make([]view.MonitoringTrafficRowView, 0, len(rows))
	for _, row := range rows {
		label := row.Label
		isLatest := false
		switch label {
		case latestLabel:
			isLatest = true
			if len(label) == 10 {
				label = "Today"
			} else {
				label = "This month"
			}
		case secondLabel:
			if secondLabel != "" {
				label = "Yesterday"
			}
		}

		bar := 0
		if maxTotal > 0 {
			bar = int(float64(row.TotalBytes) / float64(maxTotal) * 100)
			if bar < 1 && row.TotalBytes > 0 {
				bar = 1
			}
		}

		result = append(result, view.MonitoringTrafficRowView{
			Label:    label,
			RX:       formatBytes(row.RXBytes),
			TX:       formatBytes(row.TXBytes),
			Total:    formatBytes(row.TotalBytes),
			Bar:      bar,
			IsLatest: isLatest,
		})
	}
	return result
}

func reverseTrafficRowViews(rows []view.MonitoringTrafficRowView) {
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
}

func pathID(r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

func parseDurationField(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed, nil
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, nil
	}
	return 0, fmt.Errorf("invalid duration %q", value)
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func fallbackDisplay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func (v ValidationErrors) HasAny() bool {
	return len(v) > 0
}

func isSafeTrafficInterface(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '.', '-', '_', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func formatMbps(mbps float64) string {
	if mbps <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f Mbps", mbps)
}

func formatBytes(value int64) string {
	if value < 0 {
		value = 0
	}

	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := units[0]
	for index := 0; index < len(units)-1 && size >= 1024; index++ {
		size = size / 1024
		unit = units[index+1]
	}
	return fmt.Sprintf("%.2f %s", size, unit)
}
