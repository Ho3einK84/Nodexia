package alerts

import (
	"context"
	"net/http"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/notify"
	"github.com/Ho3einK84/Nodexia/internal/view"
)

// Handlers groups every alerts HTTP handler around the shared dependencies and
// repositories. The servers repository is used only to resolve server names for
// labels and dropdowns, keeping this module decoupled from server internals.
// notifier is nil when no Telegram bot token is configured; the UI then shows a
// "not configured" notice instead of attempting to send.
type Handlers struct {
	deps       module.Dependencies
	repo       Repository
	serverRepo servers.Repository
	notifier   notify.Notifier
}

func NewHandlers(deps module.Dependencies, repo Repository, serverRepo servers.Repository, notifier notify.Notifier) Handlers {
	return Handlers{deps: deps, repo: repo, serverRepo: serverRepo, notifier: notifier}
}

// tokenConfigured reports whether a notifier is available to send messages.
func (h Handlers) tokenConfigured() bool {
	return h.notifier != nil
}

// ── Overview ─────────────────────────────────────────────────────────────────

func (h Handlers) Overview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.overviewModel(r.Context())
	if err != nil {
		renderError(w, h.deps, err, "Could not load alerts", "The alerts page could not be loaded.")
		return
	}
	renderOverview(w, r, h.deps, http.StatusOK, overview, flashKind(r), flashMessage(r))
}

// overviewModel loads every section of the alerts overview in one place so the
// page, the inline-error path, and the channel test action share one builder.
func (h Handlers) overviewModel(ctx context.Context) (view.AlertsOverviewView, error) {
	rules, err := h.repo.ListRules(ctx)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}
	channels, err := h.repo.ListChannels(ctx)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}
	silences, err := h.repo.ListSilences(ctx)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}
	events, err := h.repo.ListRecentEvents(ctx, recentEventLimit)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}
	refs, err := h.loadServerRefs(ctx)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}

	ruleIDs := make([]int64, len(rules))
	for i, r := range rules {
		ruleIDs[i] = r.ID
	}
	streaks, err := h.repo.ListStreaksForRules(ctx, ruleIDs)
	if err != nil {
		streaks = map[streakKey]int{} // non-fatal: overview renders without streak info
	}

	return buildOverview(rules, channels, silences, events, refs, streaks, h.tokenConfigured(), time.Now().UTC()), nil
}

// recentEventLimit caps how many alert history rows the overview shows.
const recentEventLimit = 20

// ── Rules ────────────────────────────────────────────────────────────────────

func (h Handlers) RuleNew(w http.ResponseWriter, r *http.Request) {
	refs, channels, ok := h.loadRuleFormDeps(w, r)
	if !ok {
		return
	}
	form := buildRuleFormView(0, DefaultRuleFormInput(), ValidationErrors{}, "/alerts/rules", "", refs, channels)
	renderRuleForm(w, r, h.deps, http.StatusOK, "New alert rule",
		"Fire a notification when a metric crosses a threshold. Leave the server unset to apply the rule to every server.",
		form, "", "")
}

func (h Handlers) RuleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted alert rule form could not be parsed.")
		return
	}
	refs, channels, ok := h.loadRuleFormDeps(w, r)
	if !ok {
		return
	}

	validated, errs := ValidateRuleForm(ruleFormInputFromRequest(r))
	h.checkRuleAssociations(validated.Rule, refs, channels, errs)
	if errs.HasAny() {
		form := buildRuleFormView(0, validated.Input, errs, "/alerts/rules", "", refs, channels)
		renderRuleForm(w, r, h.deps, http.StatusUnprocessableEntity, "New alert rule",
			"Fix the highlighted fields and create the rule again.", form, "error",
			"Please fix the highlighted fields before saving.")
		return
	}

	if _, err := h.repo.CreateRule(r.Context(), validated.Rule); err != nil {
		renderError(w, h.deps, err, "Could not create rule", "The alert rule could not be created.")
		return
	}
	http.Redirect(w, r, redirectURL("rule-created"), http.StatusSeeOther)
}

func (h Handlers) RuleEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Rule not found", "The requested alert rule does not exist.")
		return
	}
	rule, err := h.repo.GetRule(r.Context(), id)
	if err != nil {
		renderError(w, h.deps, err, "Could not load rule", "The requested alert rule could not be loaded.")
		return
	}
	refs, channels, ok := h.loadRuleFormDeps(w, r)
	if !ok {
		return
	}

	form := buildRuleFormView(
		rule.ID,
		RuleFormInputFromRule(rule),
		ValidationErrors{},
		"/alerts/rules/"+formatID(rule.ID)+"/edit",
		"/alerts/rules/"+formatID(rule.ID)+"/delete",
		refs, channels,
	)
	renderRuleForm(w, r, h.deps, http.StatusOK, "Edit alert rule",
		"Update the threshold, channel, or noise controls for this rule.", form, "", "")
}

func (h Handlers) RuleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Rule not found", "The requested alert rule does not exist.")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted alert rule form could not be parsed.")
		return
	}
	refs, channels, ok := h.loadRuleFormDeps(w, r)
	if !ok {
		return
	}

	validated, errs := ValidateRuleForm(ruleFormInputFromRequest(r))
	validated.Rule.ID = id
	h.checkRuleAssociations(validated.Rule, refs, channels, errs)
	if errs.HasAny() {
		form := buildRuleFormView(
			id, validated.Input, errs,
			"/alerts/rules/"+formatID(id)+"/edit",
			"/alerts/rules/"+formatID(id)+"/delete",
			refs, channels,
		)
		renderRuleForm(w, r, h.deps, http.StatusUnprocessableEntity, "Edit alert rule",
			"Fix the highlighted fields and save the rule again.", form, "error",
			"Please fix the highlighted fields before saving.")
		return
	}

	if _, err := h.repo.UpdateRule(r.Context(), validated.Rule); err != nil {
		renderError(w, h.deps, err, "Could not update rule", "The alert rule could not be updated.")
		return
	}
	http.Redirect(w, r, redirectURL("rule-updated"), http.StatusSeeOther)
}

func (h Handlers) RuleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Rule not found", "The requested alert rule does not exist.")
		return
	}
	if err := h.repo.DeleteRule(r.Context(), id); err != nil {
		renderError(w, h.deps, err, "Could not delete rule", "The alert rule could not be deleted.")
		return
	}
	http.Redirect(w, r, redirectURL("rule-deleted"), http.StatusSeeOther)
}

// ── Channels ─────────────────────────────────────────────────────────────────

func (h Handlers) ChannelNew(w http.ResponseWriter, r *http.Request) {
	form := buildChannelFormView(0, DefaultChannelFormInput(), ValidationErrors{}, "/alerts/channels", "")
	renderChannelForm(w, r, h.deps, http.StatusOK, "New channel",
		"Route alert notifications to a Telegram chat or channel.", form, "", "")
}

func (h Handlers) ChannelCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted channel form could not be parsed.")
		return
	}

	validated, errs := ValidateChannelForm(channelFormInputFromRequest(r))
	if errs.HasAny() {
		form := buildChannelFormView(0, validated.Input, errs, "/alerts/channels", "")
		renderChannelForm(w, r, h.deps, http.StatusUnprocessableEntity, "New channel",
			"Fix the highlighted fields and create the channel again.", form, "error",
			"Please fix the highlighted fields before saving.")
		return
	}

	if _, err := h.repo.CreateChannel(r.Context(), validated.Channel); err != nil {
		renderError(w, h.deps, err, "Could not create channel", "The notification channel could not be created.")
		return
	}
	http.Redirect(w, r, redirectURL("channel-created"), http.StatusSeeOther)
}

func (h Handlers) ChannelEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Channel not found", "The requested channel does not exist.")
		return
	}
	channel, err := h.repo.GetChannel(r.Context(), id)
	if err != nil {
		renderError(w, h.deps, err, "Could not load channel", "The requested channel could not be loaded.")
		return
	}

	form := buildChannelFormView(
		channel.ID,
		ChannelFormInputFromChannel(channel),
		ValidationErrors{},
		"/alerts/channels/"+formatID(channel.ID)+"/edit",
		"/alerts/channels/"+formatID(channel.ID)+"/delete",
	)
	renderChannelForm(w, r, h.deps, http.StatusOK, "Edit channel",
		"Update where this channel delivers notifications.", form, "", "")
}

func (h Handlers) ChannelUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Channel not found", "The requested channel does not exist.")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted channel form could not be parsed.")
		return
	}

	validated, errs := ValidateChannelForm(channelFormInputFromRequest(r))
	validated.Channel.ID = id
	if errs.HasAny() {
		form := buildChannelFormView(
			id, validated.Input, errs,
			"/alerts/channels/"+formatID(id)+"/edit",
			"/alerts/channels/"+formatID(id)+"/delete",
		)
		renderChannelForm(w, r, h.deps, http.StatusUnprocessableEntity, "Edit channel",
			"Fix the highlighted fields and save the channel again.", form, "error",
			"Please fix the highlighted fields before saving.")
		return
	}

	if _, err := h.repo.UpdateChannel(r.Context(), validated.Channel); err != nil {
		renderError(w, h.deps, err, "Could not update channel", "The notification channel could not be updated.")
		return
	}
	http.Redirect(w, r, redirectURL("channel-updated"), http.StatusSeeOther)
}

func (h Handlers) ChannelDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Channel not found", "The requested channel does not exist.")
		return
	}
	if err := h.repo.DeleteChannel(r.Context(), id); err != nil {
		renderError(w, h.deps, err, "Could not delete channel", "The notification channel could not be deleted.")
		return
	}
	http.Redirect(w, r, redirectURL("channel-deleted"), http.StatusSeeOther)
}

// ChannelTest sends a fixed sample message to a channel's chat id using the
// configured Telegram bot token, then re-renders the alerts overview with a
// success or error flash. When no token is configured it shows a notice instead
// of erroring.
func (h Handlers) ChannelTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Channel not found", "The requested channel does not exist.")
		return
	}
	channel, err := h.repo.GetChannel(r.Context(), id)
	if err != nil {
		renderError(w, h.deps, err, "Could not load channel", "The requested channel could not be loaded.")
		return
	}

	kind, message, statusCode := h.sendTestMessage(r.Context(), channel)

	overview, err := h.overviewModel(r.Context())
	if err != nil {
		renderError(w, h.deps, err, "Could not load alerts", "The alerts page could not be loaded.")
		return
	}
	renderOverview(w, r, h.deps, statusCode, overview, kind, message)
}

// sendTestMessage renders and dispatches a sample alert to the channel, mapping
// the outcome to a flash kind, message, and HTTP status. The Telegram client
// redacts the bot token from any error, so the message is safe to surface.
func (h Handlers) sendTestMessage(ctx context.Context, channel Channel) (kind, message string, statusCode int) {
	if !h.tokenConfigured() {
		return "warn", "Telegram bot token not configured. Set NODEXIA_TELEGRAM_BOT_TOKEN to send test messages.", http.StatusOK
	}

	text, err := notify.RenderMessage(channel.MessageTemplate, sampleAlertMessage())
	if err != nil {
		return "error", "Could not render the test message — check this channel's template: " + err.Error(), http.StatusUnprocessableEntity
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := h.notifier.Send(sendCtx, channel.ChatID, text); err != nil {
		return "error", "Test message to " + channel.Name + " failed: " + err.Error(), http.StatusBadGateway
	}

	return "success", "Test message sent to " + channel.Name + ".", http.StatusOK
}

// sampleAlertMessage is the canned alert used by the channel test action.
func sampleAlertMessage() notify.AlertMessage {
	return notify.AlertMessage{
		Server:    "example-server",
		Metric:    MetricLabel(MetricCPU),
		Value:     "93%",
		Threshold: ComparatorSymbol(ComparatorGTE) + " 90%",
		Severity:  SeverityWarning,
		FiredAt:   time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
	}
}

// ── Silences ─────────────────────────────────────────────────────────────────

func (h Handlers) SilenceCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted silence form could not be parsed.")
		return
	}

	input := silenceFormInputFromRequest(r)
	validated, errs := ValidateSilenceForm(input, time.Now())
	if !errs.HasAny() {
		if _, err := h.serverRepo.GetByID(r.Context(), validated.Silence.ServerID); err != nil {
			errs.Add("server_id", "Select an existing server.")
		}
	}

	if errs.HasAny() {
		h.renderOverviewWithSilenceErrors(w, r, input, errs)
		return
	}

	if _, err := h.repo.CreateSilence(r.Context(), validated.Silence); err != nil {
		renderError(w, h.deps, err, "Could not create silence", "The silence could not be created.")
		return
	}
	http.Redirect(w, r, redirectURL("silenced"), http.StatusSeeOther)
}

func (h Handlers) SilenceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, ErrNotFound, "Silence not found", "The requested silence does not exist.")
		return
	}
	if err := h.repo.DeleteSilence(r.Context(), id); err != nil {
		renderError(w, h.deps, err, "Could not remove silence", "The silence could not be removed.")
		return
	}
	http.Redirect(w, r, redirectURL("silence-removed"), http.StatusSeeOther)
}

// ServerSilence is the one-click "mute this metric for this server" action used
// from a server-scoped page. It inserts (or refreshes) an indefinite silence.
func (h Handlers) ServerSilence(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderError(w, h.deps, servers.ErrNotFound, "Server not found", "The requested server does not exist.")
		return
	}
	if _, err := h.serverRepo.GetByID(r.Context(), serverID); err != nil {
		renderError(w, h.deps, err, "Could not load server", "The selected server could not be loaded.")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, h.deps, err, "Invalid form request", "The submitted silence form could not be parsed.")
		return
	}

	input := SilenceFormInput{
		ServerID: formatID(serverID),
		Metric:   r.FormValue("metric"),
		Reason:   r.FormValue("reason"),
	}
	validated, errs := ValidateSilenceForm(input, time.Now())
	if errs.HasAny() {
		renderError(w, h.deps, ErrNotFound, "Invalid silence", "The metric to silence is missing or unsupported.")
		return
	}

	if _, err := h.repo.CreateSilence(r.Context(), validated.Silence); err != nil {
		renderError(w, h.deps, err, "Could not create silence", "The silence could not be created.")
		return
	}
	http.Redirect(w, r, redirectURL("silenced"), http.StatusSeeOther)
}

// ── Shared helpers ───────────────────────────────────────────────────────────

func (h Handlers) renderOverviewWithSilenceErrors(w http.ResponseWriter, r *http.Request, input SilenceFormInput, errs ValidationErrors) {
	ctx := r.Context()
	overview, err := h.overviewModel(ctx)
	if err != nil {
		renderError(w, h.deps, err, "Could not load alerts", "The alerts page could not be loaded.")
		return
	}
	refs, err := h.loadServerRefs(ctx)
	if err != nil {
		renderError(w, h.deps, err, "Could not load servers", "The server registry could not be loaded.")
		return
	}
	overview.SilenceForm = buildSilenceFormView(input, errs, refs)

	renderOverview(w, r, h.deps, http.StatusUnprocessableEntity, overview, "error",
		"Please fix the highlighted fields before muting.")
}

func (h Handlers) loadServerRefs(ctx context.Context) ([]serverRef, error) {
	list, err := h.serverRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]serverRef, 0, len(list))
	for _, server := range list {
		refs = append(refs, serverRef{ID: server.ID, Name: server.Name})
	}
	return refs, nil
}

// loadRuleFormDeps loads the servers and channels needed to render a rule form.
// It renders an error page and returns ok=false on failure.
func (h Handlers) loadRuleFormDeps(w http.ResponseWriter, r *http.Request) ([]serverRef, []Channel, bool) {
	refs, err := h.loadServerRefs(r.Context())
	if err != nil {
		renderError(w, h.deps, err, "Could not load servers", "The server registry could not be loaded.")
		return nil, nil, false
	}
	channels, err := h.repo.ListChannels(r.Context())
	if err != nil {
		renderError(w, h.deps, err, "Could not load channels", "The notification channels could not be loaded.")
		return nil, nil, false
	}
	return refs, channels, true
}

// checkRuleAssociations records a field error when a chosen server or channel no
// longer exists, since foreign keys are not enforced at the engine level.
func (h Handlers) checkRuleAssociations(rule Rule, refs []serverRef, channels []Channel, errs ValidationErrors) {
	if rule.ServerID != nil && !serverRefExists(refs, *rule.ServerID) {
		errs.Add("server_id", "Select an existing server.")
	}
	if rule.ChannelID != nil && !channelExists(channels, *rule.ChannelID) {
		errs.Add("channel_id", "Select an existing channel.")
	}
}

func serverRefExists(refs []serverRef, id int64) bool {
	for _, ref := range refs {
		if ref.ID == id {
			return true
		}
	}
	return false
}

func channelExists(channels []Channel, id int64) bool {
	for _, channel := range channels {
		if channel.ID == id {
			return true
		}
	}
	return false
}

func ruleFormInputFromRequest(r *http.Request) RuleFormInput {
	return RuleFormInput{
		ServerID:        r.FormValue("server_id"),
		Metric:          r.FormValue("metric"),
		Comparator:      r.FormValue("comparator"),
		Threshold:       r.FormValue("threshold"),
		ConsecutiveHits: r.FormValue("consecutive_hits"),
		CooldownSeconds: r.FormValue("cooldown_seconds"),
		Severity:        r.FormValue("severity"),
		ChannelID:       r.FormValue("channel_id"),
		Enabled:         r.FormValue("enabled") != "",
		Note:            r.FormValue("note"),
	}
}

func channelFormInputFromRequest(r *http.Request) ChannelFormInput {
	return ChannelFormInput{
		Kind:            r.FormValue("kind"),
		Name:            r.FormValue("name"),
		ChatID:          r.FormValue("chat_id"),
		MessageTemplate: r.FormValue("message_template"),
		Enabled:         r.FormValue("enabled") != "",
	}
}

func silenceFormInputFromRequest(r *http.Request) SilenceFormInput {
	return SilenceFormInput{
		ServerID:     r.FormValue("server_id"),
		Metric:       r.FormValue("metric"),
		Reason:       r.FormValue("reason"),
		ExpiresHours: r.FormValue("expires_hours"),
	}
}
