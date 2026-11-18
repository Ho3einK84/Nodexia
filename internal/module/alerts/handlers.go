package alerts

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ho3einK84/Nodexia/internal/i18n"
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
	eventsPage, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("events_page")))
	overview, err := h.overviewModel(r.Context(), eventsPage)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_title", "alerts.error.load_message")
		return
	}
	renderOverview(w, r, h.deps, http.StatusOK, overview, flashKind(r), flashMessage(r))
}

// overviewModel loads every section of the alerts overview in one place so the
// page, the inline-error path, and the channel test action share one builder.
// eventsPage selects which page of the alert history renders (clamped to the
// available range; pass 1 for the first page).
func (h Handlers) overviewModel(ctx context.Context, eventsPage int) (view.AlertsOverviewView, error) {
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

	eventsTotal, err := h.repo.CountEvents(ctx)
	if err != nil {
		return view.AlertsOverviewView{}, err
	}
	totalPages := (eventsTotal + recentEventsPerPage - 1) / recentEventsPerPage
	if totalPages < 1 {
		totalPages = 1
	}
	if eventsPage < 1 {
		eventsPage = 1
	}
	if eventsPage > totalPages {
		eventsPage = totalPages
	}
	events, err := h.repo.ListEventsPage(ctx, recentEventsPerPage, (eventsPage-1)*recentEventsPerPage)
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

	overview := buildOverview(rules, channels, silences, events, refs, streaks, h.tokenConfigured(), time.Now().UTC())
	overview.EventsTotal = eventsTotal
	overview.EventsPagination = buildEventsPagination(eventsPage, totalPages)
	return overview, nil
}

// recentEventsPerPage caps how many alert history rows render per page.
const recentEventsPerPage = 10

// ── Rules ────────────────────────────────────────────────────────────────────

func (h Handlers) RuleNew(w http.ResponseWriter, r *http.Request) {
	refs, channels, ok := h.loadRuleFormDeps(w, r)
	if !ok {
		return
	}
	form := buildRuleFormView(0, DefaultRuleFormInput(), ValidationErrors{}, "/alerts/rules", "", refs, channels)
	renderRuleForm(w, r, h.deps, http.StatusOK, "alerts.rule_form_new", "alerts.rule_form_new_desc",
		form, "", "")
}

func (h Handlers) RuleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_rule_message")
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
		renderRuleForm(w, r, h.deps, http.StatusUnprocessableEntity, "alerts.rule_form_new", "alerts.rule_form_fix_create", form, "error",
			"alerts.fix_fields")
		return
	}

	if _, err := h.repo.CreateRule(r.Context(), validated.Rule); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.create_rule_title", "alerts.error.create_rule_message")
		return
	}
	http.Redirect(w, r, redirectURL("rule-created"), http.StatusSeeOther)
}

func (h Handlers) RuleEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.rule_not_found_title", "alerts.error.rule_not_found_message")
		return
	}
	rule, err := h.repo.GetRule(r.Context(), id)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_rule_title", "alerts.error.load_rule_message")
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
	renderRuleForm(w, r, h.deps, http.StatusOK, "alerts.rule_form_edit",
		"alerts.rule_form_edit_desc", form, "", "")
}

func (h Handlers) RuleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.rule_not_found_title", "alerts.error.rule_not_found_message")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_rule_message")
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
		renderRuleForm(w, r, h.deps, http.StatusUnprocessableEntity, "alerts.rule_form_edit",
			"alerts.rule_form_fix_save", form, "error",
			"alerts.fix_fields")
		return
	}

	if _, err := h.repo.UpdateRule(r.Context(), validated.Rule); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.update_rule_title", "alerts.error.update_rule_message")
		return
	}
	http.Redirect(w, r, redirectURL("rule-updated"), http.StatusSeeOther)
}

func (h Handlers) RuleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.rule_not_found_title", "alerts.error.rule_not_found_message")
		return
	}
	if err := h.repo.DeleteRule(r.Context(), id); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.delete_rule_title", "alerts.error.delete_rule_message")
		return
	}
	http.Redirect(w, r, redirectURL("rule-deleted"), http.StatusSeeOther)
}

// ── Channels ─────────────────────────────────────────────────────────────────

func (h Handlers) ChannelNew(w http.ResponseWriter, r *http.Request) {
	form := buildChannelFormView(0, DefaultChannelFormInput(), ValidationErrors{}, "/alerts/channels", "")
	renderChannelForm(w, r, h.deps, http.StatusOK, "alerts.channel_form_new",
		"alerts.channel_form_new_desc", form, "", "")
}

func (h Handlers) ChannelCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_channel_message")
		return
	}

	validated, errs := ValidateChannelForm(channelFormInputFromRequest(r))
	if errs.HasAny() {
		form := buildChannelFormView(0, validated.Input, errs, "/alerts/channels", "")
		renderChannelForm(w, r, h.deps, http.StatusUnprocessableEntity, "alerts.channel_form_new",
			"alerts.channel_form_fix_create", form, "error",
			"alerts.fix_fields")
		return
	}

	if _, err := h.repo.CreateChannel(r.Context(), validated.Channel); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.create_channel_title", "alerts.error.create_channel_message")
		return
	}
	http.Redirect(w, r, redirectURL("channel-created"), http.StatusSeeOther)
}

func (h Handlers) ChannelEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.channel_not_found_title", "alerts.error.channel_not_found_message")
		return
	}
	channel, err := h.repo.GetChannel(r.Context(), id)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_channel_title", "alerts.error.load_channel_message")
		return
	}

	form := buildChannelFormView(
		channel.ID,
		ChannelFormInputFromChannel(channel),
		ValidationErrors{},
		"/alerts/channels/"+formatID(channel.ID)+"/edit",
		"/alerts/channels/"+formatID(channel.ID)+"/delete",
	)
	renderChannelForm(w, r, h.deps, http.StatusOK, "alerts.channel_form_edit",
		"alerts.channel_form_edit_desc", form, "", "")
}

func (h Handlers) ChannelUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.channel_not_found_title", "alerts.error.channel_not_found_message")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_channel_message")
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
		renderChannelForm(w, r, h.deps, http.StatusUnprocessableEntity, "alerts.channel_form_edit",
			"alerts.channel_form_fix_save", form, "error",
			"alerts.fix_fields")
		return
	}

	if _, err := h.repo.UpdateChannel(r.Context(), validated.Channel); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.update_channel_title", "alerts.error.update_channel_message")
		return
	}
	http.Redirect(w, r, redirectURL("channel-updated"), http.StatusSeeOther)
}

func (h Handlers) ChannelDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.channel_not_found_title", "alerts.error.channel_not_found_message")
		return
	}
	if err := h.repo.DeleteChannel(r.Context(), id); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.delete_channel_title", "alerts.error.delete_channel_message")
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
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.channel_not_found_title", "alerts.error.channel_not_found_message")
		return
	}
	channel, err := h.repo.GetChannel(r.Context(), id)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_channel_title", "alerts.error.load_channel_message")
		return
	}

	kind, message, statusCode := h.sendTestMessage(r.Context(), localizerFromRequest(r), channel)

	overview, err := h.overviewModel(r.Context(), 1)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_title", "alerts.error.load_message")
		return
	}
	renderOverview(w, r, h.deps, statusCode, overview, kind, message)
}

// sendTestMessage renders and dispatches a sample alert to the channel, mapping
// the outcome to a flash kind, message, and HTTP status. The Telegram client
// redacts the bot token from any error, so the message is safe to surface.
func (h Handlers) sendTestMessage(ctx context.Context, loc *i18n.Localizer, channel Channel) (kind, message string, statusCode int) {
	if !h.tokenConfigured() {
		return "warn", loc.T("alerts.test.no_token"), http.StatusOK
	}

	text, err := notify.RenderMessage(channel.MessageTemplate, sampleAlertMessage())
	if err != nil {
		return "error", loc.T("alerts.test.render_failed", "error", err.Error()), http.StatusUnprocessableEntity
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := h.notifier.Send(sendCtx, channel.ChatID, text); err != nil {
		return "error", loc.T("alerts.test.send_failed", "name", channel.Name, "error", err.Error()), http.StatusBadGateway
	}

	return "success", loc.T("alerts.test.sent", "name", channel.Name), http.StatusOK
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
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_silence_message")
		return
	}

	input := silenceFormInputFromRequest(r)
	validated, errs := ValidateSilenceForm(input, time.Now())
	if !errs.HasAny() {
		if _, err := h.serverRepo.GetByID(r.Context(), validated.Silence.ServerID); err != nil {
			errs.Add("server_id", localizerFromRequest(r).T("alerts.validation.server_exists"))
		}
	}

	if errs.HasAny() {
		h.renderOverviewWithSilenceErrors(w, r, input, errs)
		return
	}

	if _, err := h.repo.CreateSilence(r.Context(), validated.Silence); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.create_silence_title", "alerts.error.create_silence_message")
		return
	}
	http.Redirect(w, r, redirectURL("silenced"), http.StatusSeeOther)
}

func (h Handlers) SilenceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.silence_not_found_title", "alerts.error.silence_not_found_message")
		return
	}
	if err := h.repo.DeleteSilence(r.Context(), id); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.remove_silence_title", "alerts.error.remove_silence_message")
		return
	}
	http.Redirect(w, r, redirectURL("silence-removed"), http.StatusSeeOther)
}

// ServerSilence is the one-click "mute this metric for this server" action used
// from a server-scoped page. It inserts (or refreshes) an indefinite silence.
func (h Handlers) ServerSilence(w http.ResponseWriter, r *http.Request) {
	serverID, ok := pathID(r)
	if !ok {
		renderError(w, r, h.deps, servers.ErrNotFound, "alerts.error.server_not_found_title", "alerts.error.server_not_found_message")
		return
	}
	if _, err := h.serverRepo.GetByID(r.Context(), serverID); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_server_title", "alerts.error.load_server_message")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.invalid_form_title", "alerts.error.invalid_silence_message")
		return
	}

	input := SilenceFormInput{
		ServerID: formatID(serverID),
		Metric:   r.FormValue("metric"),
		Reason:   r.FormValue("reason"),
	}
	validated, errs := ValidateSilenceForm(input, time.Now())
	if errs.HasAny() {
		renderError(w, r, h.deps, ErrNotFound, "alerts.error.invalid_silence_title", "alerts.error.invalid_silence_metric")
		return
	}

	if _, err := h.repo.CreateSilence(r.Context(), validated.Silence); err != nil {
		renderError(w, r, h.deps, err, "alerts.error.create_silence_title", "alerts.error.create_silence_message")
		return
	}
	http.Redirect(w, r, redirectURL("silenced"), http.StatusSeeOther)
}

// ── Shared helpers ───────────────────────────────────────────────────────────

func (h Handlers) renderOverviewWithSilenceErrors(w http.ResponseWriter, r *http.Request, input SilenceFormInput, errs ValidationErrors) {
	ctx := r.Context()
	overview, err := h.overviewModel(ctx, 1)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_title", "alerts.error.load_message")
		return
	}
	refs, err := h.loadServerRefs(ctx)
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_servers_title", "alerts.error.load_servers_message")
		return
	}
	overview.SilenceForm = buildSilenceFormView(input, errs, refs)

	renderOverview(w, r, h.deps, http.StatusUnprocessableEntity, overview, "error",
		"alerts.fix_fields_mute")
}

func (h Handlers) loadServerRefs(ctx context.Context) ([]serverRef, error) {
	list, err := h.serverRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]serverRef, 0, len(list))
	for _, server := range list {
		refs = append(refs, serverRef{ID: server.ID, Name: server.Name, CountryCode: server.CountryCode})
	}
	return refs, nil
}

// loadRuleFormDeps loads the servers and channels needed to render a rule form.
// It renders an error page and returns ok=false on failure.
func (h Handlers) loadRuleFormDeps(w http.ResponseWriter, r *http.Request) ([]serverRef, []Channel, bool) {
	refs, err := h.loadServerRefs(r.Context())
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_servers_title", "alerts.error.load_servers_message")
		return nil, nil, false
	}
	channels, err := h.repo.ListChannels(r.Context())
	if err != nil {
		renderError(w, r, h.deps, err, "alerts.error.load_channels_title", "alerts.error.load_channels_message")
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
