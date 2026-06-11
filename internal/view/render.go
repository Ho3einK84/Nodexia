package view

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	assets "github.com/Ho3einK84/Nodexia"
)

type PageData struct {
	// CSRFToken is the synchronizer token that must be embedded in every HTML
	// form as a hidden field named "_csrf_token".  Handlers populate this field
	// by calling middleware.GetCSRFToken(r.Context()) after the Session middleware
	// has run.
	CSRFToken   string
	// PageStyles / PageScripts are per-page asset URLs injected by the layout.
	PageStyles  []string
	PageScripts []string
	Title       string
	Description       string
	ContentTemplate   string
	MainHTML          template.HTML
	AppName           string
	Environment       string
	Version           string
	HTTPAddress       string
	DatabaseDriver    string
	DatabaseTarget    string
	MigrationCount    int
	EnvFile           string
	ActiveNav         string
	NavigationItems   []NavItem
	PageTitle         string
	PageDescription   string
	StatusCode        int
	ErrorTitle        string
	ErrorMessage      string
	RequestID         string
	ErrorDetail       string
	Diagnostics       DiagnosticsView
	FooterNote        string
	RouteGroups       []string
	ModuleName        string
	ModuleRouteGroup  string
	ModuleDescription string
	FlashKind         string
	FlashMessage      string
	ServerCount       int
	Servers           []ServerSummary
	ServerSearch      string
	ServerMatchCount  int
	ServerShowingFrom int
	ServerShowingTo   int
	ServerPagination  PaginationView
	ServerForm        ServerFormView
	IsEditingServer   bool
	ServerFormAction  string
	ServerDeleteAction string
	CommandTarget     CommandTargetView
	CommandForm       CommandFormView
	CommandPresets    []CommandPresetView
	CommandHistory    []CommandHistoryView
	CommandResult     CommandResultView
	ConnectionResult  ConnectionTestView
	CommandStream     CommandStreamView
	FileTarget        FileTargetView
	FileForm          FileFormView
	FileListing       FileListingView
	FileDownload      FileDownloadView
	SystemTarget      SystemTargetView
	SystemForm        SystemFormView
	SystemFacts       SystemSnapshotView
	SystemCollection  SystemCollectionResultView
	MonitoringTarget     MonitoringTargetView
	MonitoringForm       MonitoringFormView
	MonitoringSnapshot   MonitoringSnapshotView
	MonitoringCollection MonitoringCollectionResultView
	MonitoringTraffic           MonitoringTrafficSnapshotView
	MonitoringTrafficCollection MonitoringTrafficCollectionResultView
	DashboardSnapshots          []DashboardMonitoringView
	DashboardSnapshotTotal      int
	DashboardSnapshotPagination PaginationView
	SchedulerOverview           SchedulerOverviewView
	NodeTarget           NodeTargetView
	NodeForm             NodeFormView
	NodeSnapshots        []NodeSnapshotView
	NodeCollection       NodeCollectionResultView
}

// PaginationView describes a rendered pagination control. It is reusable across
// any paginated list (servers today, more later).
type PaginationView struct {
	CurrentPage int
	TotalPages  int
	HasPrev     bool
	HasNext     bool
	PrevURL     string
	NextURL     string
	Pages       []PaginationPageView
}

type PaginationPageView struct {
	Number   int
	URL      string
	IsActive bool
	IsGap    bool
}

type ServerSummary struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Note               string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	CreatedAt          string
	UpdatedAt          string
}

type ServerFormView struct {
	ID                 int64
	Name               string
	Host               string
	Port               string
	AuthMode           string
	Username           string
	Tags               string
	Note               string
	CredentialStrategy string
	CredentialRef      string
	Errors             map[string]string
}

type CommandTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	UpdatedAt          string
}

type CommandFormView struct {
	Action                    string
	Intent                    string
	Command                   string
	ConnectTimeout            string
	CommandTimeout            string
	StoredCredentialsAvailable bool
	RefreshURL                string
	Errors                    map[string]string
}

type CommandPresetView struct {
	Key         string
	Label       string
	Description string
	Command     string
	Href        string
}

type CommandHistoryView struct {
	ID         int64
	Command    string
	ExitCode   string
	Stdout     string
	Stderr     string
	ExecutedAt string
}

type CommandResultView struct {
	Available  bool
	Command    string
	ExitCode   string
	Duration   string
	ExecutedAt string
	Stdout     string
	Stderr     string
	Error      string
}

type ConnectionTestView struct {
	Available bool
	Duration  string
	Message   string
	Error     string
}

type CommandStreamView struct {
	Available     bool
	ID            string
	Status        string
	IsRunning     bool
	Command       string
	ExitCode      string
	StartedAt     string
	UpdatedAt     string
	CompletedAt   string
	Duration      string
	Stdout        string
	Stderr        string
	Error         string
	HistoryID     int64
	RefreshURL    string
	RefreshMillis int
}

type FileTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	UpdatedAt          string
}

type FileFormView struct {
	Action                    string
	Path                      string
	ConnectTimeout            string
	Password                  string
	PrivateKey                string
	KeyPassphrase             string
	StoredCredentialsAvailable bool
	RefreshURL                string
	Errors                    map[string]string
}

type FileEntryView struct {
	Name       string
	Path       string
	Kind       string
	Size       string
	Mode       string
	ModifiedAt string
}

type FileListingView struct {
	Available bool
	Path      string
	Parent    string
	Entries   []FileEntryView
}

type FileDownloadView struct {
	Available  bool
	Path       string
	Name       string
	Size       string
	ModifiedAt string
	Message    string
	Error      string
}

type SchedulerOverviewView struct {
	Enabled            bool
	StartupDelay       string
	SweepInterval      string
	MonitoringInterval string
	NodesInterval      string
	RetryBackoff       string
	EligibleJobs       int
	BlockedJobs        int
	RunningJobs        int
	Jobs               []ScheduledJobView
	MoreJobs           int
	Pagination         PaginationView
}

type ScheduledJobView struct {
	ServerID            int64
	ServerName          string
	JobType             string
	Status              string
	Detail              string
	LastError           string
	NextRunAt           string
	LastStartedAt       string
	LastSuccessAt       string
	LastDuration        string
	ConsecutiveFailures int
}

type SystemTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	UpdatedAt          string
}

type SystemFormView struct {
	Action                    string
	ConnectTimeout            string
	CommandTimeout            string
	StoredCredentialsAvailable bool
	RefreshURL                string
	Errors                    map[string]string
}

type SystemSnapshotView struct {
	Available            bool
	Hostname             string
	OSName               string
	OSVersion            string
	KernelVersion        string
	Architecture         string
	UptimeHuman          string
	UptimeSeconds        string
	LastUpdateAt         string
	LastUpdateUnix       string
	CollectedAt          string
	OS                   string
	Platform             string
	PlatformFamily       string
	PlatformVersion      string
	KernelArch           string
	VirtualizationSystem string
	VirtualizationRole   string
	TotalRAM             string
}

type SystemCollectionResultView struct {
	Available   bool
	Command     string
	Duration    string
	CollectedAt string
	Stdout      string
	Stderr      string
	Error       string
}

type MonitoringTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	UpdatedAt          string
}

type MonitoringFormView struct {
	Action                    string
	ConnectTimeout            string
	CommandTimeout            string
	TrafficInterface          string
	StoredCredentialsAvailable bool
	RefreshURL                string
	Errors                    map[string]string
}

type MonitoringSnapshotView struct {
	Available      bool
	CPUUsage       string
	RAMUsage       string
	DiskUsage      string
	LoadAverage1   string
	LoadAverage5   string
	LoadAverage15  string
	UptimeHuman    string
	NetworkSummary string
	CollectedAt    string
}

type MonitoringCollectionResultView struct {
	Available   bool
	Command     string
	Duration    string
	CollectedAt string
	Stdout      string
	Stderr      string
	Error       string
}

type MonitoringTrafficSnapshotView struct {
	Known               bool
	Available           bool
	VnstatMissing       bool
	InterfaceName       string
	AvailableInterfaces []string
	Message             string
	DailyRows           []MonitoringTrafficRowView
	MonthlyRows         []MonitoringTrafficRowView
	PeakMbps            string
	AvgMbps             string
	CurrentMonthRX      string
	CollectedAt         string
}

type MonitoringTrafficCollectionResultView struct {
	Available   bool
	Command     string
	Duration    string
	CollectedAt string
	Stdout      string
	Stderr      string
	Error       string
}

type MonitoringTrafficRowView struct {
	Label    string
	RX       string
	TX       string
	Total    string
	Bar      int  // 0–100, proportional to max total in this set
	IsLatest bool // today / current month
}

type DashboardMonitoringView struct {
	ServerID        int64
	ServerName      string
	CPUUsage        string
	RAMUsage        string
	DiskUsage       string
	LoadAverage     string
	UptimeHuman     string
	NetworkSummary  string
	CollectedAt     string
	CurrentMonthDL  string
	PeakBandwidth   string
}

type NodeTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
	CredentialRef      string
	UpdatedAt          string
}

type NodeFormView struct {
	Action                    string
	ConnectTimeout            string
	CommandTimeout            string
	StoredCredentialsAvailable bool
	RefreshURL                string
	Errors                    map[string]string
}

type NodeSnapshotView struct {
	NodeType     string
	ServiceName  string
	InstallMode  string
	Version      string
	HealthStatus string
	ActivePorts  []string
	XrayPorts    []string
	ServicePort  string
	APIPort      string
	Protocol     string
	Confidence   string
	Dependencies []string
	Evidence     []string
	CollectedAt  string
}

type NodeCollectionResultView struct {
	Available   bool
	CollectedAt string
	Duration    string
	ProbeCount  int
	Probes      []NodeProbeView
	Error       string
}

type NodeProbeView struct {
	Label    string
	Command  string
	Duration string
	Stdout   string
	Stderr   string
	Error    string
}

type Renderer struct {
	templates *template.Template
}

func NewRenderer() (*Renderer, error) {
	funcMap := template.FuncMap{
		"trimSuffix": strings.TrimSuffix,
		"hasSuffix":  strings.HasSuffix,
		"float64": func(s string) float64 {
			var v float64
			fmt.Sscanf(s, "%f", &v)
			return v
		},
	}
	templates, err := template.New("").Funcs(funcMap).ParseFS(assets.Templates(), "web/templates/*.gohtml")
	if err != nil {
		return nil, err
	}

	return &Renderer{templates: templates}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, statusCode int, data PageData) error {
	data = normalizePageData(data)

	contentName := strings.TrimSpace(data.ContentTemplate)
	if contentName == "" {
		return fmt.Errorf("view: content template name is required")
	}

	var content bytes.Buffer
	if err := r.templates.ExecuteTemplate(&content, contentName, data); err != nil {
		return fmt.Errorf("view: render %s: %w", contentName, err)
	}
	data.MainHTML = template.HTML(content.String())

	var buffer bytes.Buffer
	if err := r.templates.ExecuteTemplate(&buffer, "layout", data); err != nil {
		return fmt.Errorf("view: render layout: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, err := buffer.WriteTo(w)
	return err
}

func normalizePageData(data PageData) PageData {
	if data.Title == "" {
		data.Title = data.AppName
	}

	if data.Description == "" {
		data.Description = "Lightweight SSR control plane for servers and infrastructure nodes."
	}

	if data.PageTitle == "" {
		data.PageTitle = data.Title
	}

	if data.PageDescription == "" {
		data.PageDescription = data.Description
	}

	if data.StatusCode == 0 {
		data.StatusCode = http.StatusOK
	}

	if data.FooterNote == "" {
		data.FooterNote = "Open-source, self-hosted, and optimized for an SSH-first workflow."
	}

	if len(data.NavigationItems) == 0 {
		data.NavigationItems = defaultNavigation(data.ActiveNav)
	}

	for index := range data.NavigationItems {
		data.NavigationItems[index].Active = data.NavigationItems[index].Href == data.ActiveNav
	}

	return data
}
