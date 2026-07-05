package view

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	assets "github.com/Ho3einK84/Nodexia"
	"github.com/Ho3einK84/Nodexia/internal/geoip"
	"github.com/Ho3einK84/Nodexia/internal/i18n"
)

type PageData struct {
	// CSRFToken is the synchronizer token that must be embedded in every HTML
	// form as a hidden field named "_csrf_token".  Handlers populate this field
	// by calling middleware.GetCSRFToken(r.Context()) after the Session middleware
	// has run.
	CSRFToken string
	// PageStyles / PageScripts are per-page asset URLs injected by the layout.
	PageStyles                  []string
	PageScripts                 []string
	Title                       string
	Description                 string
	ContentTemplate             string
	MainHTML                    template.HTML
	AppName                     string
	Environment                 string
	Version                     string
	HTTPAddress                 string
	DatabaseDriver              string
	DatabaseTarget              string
	MigrationCount              int
	EnvFile                     string
	ActiveNav                   string
	NavigationItems             []NavItem
	PageTitle                   string
	PageDescription             string
	PageFlag                    string // server-scoped country flag emoji; set via SetServerCountry
	PageFlagTitle               string // country name shown on the flag's hover title
	StatusCode                  int
	ErrorTitle                  string
	ErrorMessage                string
	RequestID                   string
	ErrorDetail                 string
	Diagnostics                 DiagnosticsView
	FooterNote                  string
	RouteGroups                 []string
	ModuleName                  string
	ModuleRouteGroup            string
	ModuleDescription           string
	FlashKind                   string
	FlashMessage                string
	ServerCount                 int
	TotalNodeCount              int
	Servers                     []ServerSummary
	ServerSearch                string
	ServerTagFilter             string
	ServerTagOptions            []ServerTagOption
	ServerMatchCount            int
	ServerShowingFrom           int
	ServerShowingTo             int
	ServerPagination            PaginationView
	ServerForm                  ServerFormView
	IsEditingServer             bool
	ServerFormAction            string
	ServerDeleteAction          string
	CommandTarget               CommandTargetView
	CommandForm                 CommandFormView
	CommandPresets              []CommandPresetView
	CommandHistory              []CommandHistoryView
	CommandResult               CommandResultView
	ConnectionResult            ConnectionTestView
	CommandStream               CommandStreamView
	FileTarget                  FileTargetView
	FileForm                    FileFormView
	FileListing                 FileListingView
	FileDownload                FileDownloadView
	SystemTarget                SystemTargetView
	SystemForm                  SystemFormView
	SystemFacts                 SystemSnapshotView
	SystemCollection            SystemCollectionResultView
	MonitoringTarget            MonitoringTargetView
	MonitoringForm              MonitoringFormView
	MonitoringSnapshot          MonitoringSnapshotView
	MonitoringCollection        MonitoringCollectionResultView
	MonitoringTraffic           MonitoringTrafficSnapshotView
	MonitoringTrafficCollection MonitoringTrafficCollectionResultView
	MonitoringLive              MonitoringLiveView
	DashboardSnapshots          []DashboardMonitoringView
	DashboardSnapshotTotal      int
	DashboardSnapshotPagination PaginationView
	DashboardNodeStatus         FleetNodeStatusView
	// HomeWarnings are the dismissible fleet warning banners at the top of the
	// home dashboard (exhaustion, hot resources, traffic anomalies).
	HomeWarnings []HomeWarningView
	SchedulerOverview           SchedulerOverviewView
	// BackupCanRun gates the diagnostics backup/restore section; it is false
	// when the database runtime is unavailable.
	BackupCanRun           bool
	NodeTarget             NodeTargetView
	NodeForm               NodeFormView
	NodeSnapshots          []NodeSnapshotView
	NodeCollection         NodeCollectionResultView
	NodeStream             CommandStreamView
	NodeInstallForm        NodeInstallFormView
	NodeRebeccaInstallForm NodeRebeccaInstallFormView
	NodeInstall            NodeInstallView
	AlertsOverview         AlertsOverviewView
	AlertRuleForm          AlertRuleFormView
	AlertChannelForm       AlertChannelFormView
	IsEditingAlertRule     bool
	IsEditingAlertChannel  bool

	// Bulk actions result page.
	BulkActionResult BulkActionResultView

	// Interactive SSH terminal.
	TerminalTarget TerminalTargetView
	TerminalTicket string
	TerminalForm   TerminalFormView

	// Analytics & forecasting.
	AnalyticsTarget       AnalyticsTargetView
	AnalyticsTrafficMonth AnalyticsTrafficSummaryView
	AnalyticsLimit        AnalyticsLimitView
	GlobalAnalytics       GlobalAnalyticsView
	TrafficLimits         TrafficLimitsView

	// Internationalization. Lang/Dir drive the <html lang>/<html dir>
	// attributes; LanguageOptions backs the header language switcher. localizer
	// is the active translator bound into the {{ t }}/{{ tn }} template funcs at
	// render time; it is unexported because templates resolve strings through
	// those funcs, not the field.
	Lang            string
	Dir             string
	LanguageOptions []LanguageOption
	localizer       *i18n.Localizer
}

// T resolves a translation key in the page's active language. Handlers use it
// to localize values they set on PageData (e.g. PageTitle) without reaching
// into the request context. It mirrors the {{ t }} template func and falls back
// to the default-language localizer when none is attached.
func (p *PageData) T(key string, args ...any) string {
	if p.localizer == nil {
		p.localizer = i18n.MustDefault().Localizer(i18n.DefaultLanguage)
	}
	return p.localizer.T(key, args...)
}

// LanguageOption is one entry in the header language switcher.
type LanguageOption struct {
	Code      string
	Label     string // endonym, e.g. "English", "فارسی"
	Active    bool
	SwitchURL string
}

// SetLocalizer attaches an active translator to the page and derives the
// Lang/Dir attributes and switcher options from it. Handlers call this
// indirectly via NewPageData; it is exported so request paths that build
// PageData by hand (errors, recover) can localize too.
func (p *PageData) SetLocalizer(loc *i18n.Localizer) {
	if loc == nil {
		return
	}
	p.localizer = loc
	p.Lang = loc.Lang()
	p.Dir = loc.Dir()
	options := make([]LanguageOption, 0, len(loc.Languages()))
	for _, lang := range loc.Languages() {
		options = append(options, LanguageOption{
			Code:      lang.Code,
			Label:     lang.NativeName,
			Active:    lang.Code == loc.Lang(),
			SwitchURL: "/lang/" + lang.Code,
		})
	}
	p.LanguageOptions = options
}

// SetServerCountry attaches a server's detected country to the page header so a
// flag badge renders next to the server name on any server-scoped page. It is
// safe to call with an empty/unknown code — the flag simply does not render.
func (p *PageData) SetServerCountry(code, name string) {
	p.PageFlag = geoip.FlagEmoji(code)
	p.PageFlagTitle = name
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

// ServerTagOption is one chip in the servers-list tag filter row.
type ServerTagOption struct {
	Tag    string
	Count  int
	URL    string
	Active bool
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
	// Status is the reachability state derived from the latest monitoring
	// snapshot's age relative to the monitoring interval: "up" (fresh), "down"
	// (stale — the server is not reporting), or "unknown" (never seen).
	// LastSeenAt carries a human-readable age ("4h ago") when older snapshot data
	// exists; JustNow is true when the most recent check is very recent and is
	// shown as "Just now" instead of an age.
	Status     string
	LastSeenAt string
	JustNow    bool
	// FlagEmoji is the detected country's flag (regional-indicator emoji), or ""
	// when the country is unknown/undetected. CountryName is the human-readable
	// name shown on hover; CountryCode is the ISO 3166-1 alpha-2 code.
	FlagEmoji   string
	CountryCode string
	CountryName string
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
	TrafficResetDay    string
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
	Action                     string
	Intent                     string
	Command                    string
	ConnectTimeout             string
	CommandTimeout             string
	StoredCredentialsAvailable bool
	RefreshURL                 string
	// InteractivePrograms is a space-separated list of program names that need
	// a PTY; the page JS uses it to hint that a command will open in the
	// terminal.  Generated from the server-side detector (single source of
	// truth) — the server still performs the authoritative redirect.
	InteractivePrograms string
	Errors              map[string]string
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
	Action                     string
	Path                       string
	ConnectTimeout             string
	Password                   string
	PrivateKey                 string
	KeyPassphrase              string
	StoredCredentialsAvailable bool
	RefreshURL                 string
	Errors                     map[string]string
}

type FileEntryView struct {
	Name       string
	Path       string
	Kind       string
	Size       string
	SizeBytes  int64
	Mode       string
	ModifiedAt string
	ModUnix    int64
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

// SchedulerRunView is one persisted job run row on the diagnostics page.
type SchedulerRunView struct {
	ServerName string
	JobType    string
	Success    bool
	StartedAt  string
	Duration   string
	Detail     string // message on success, error text on failure
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
	// RecentRuns is the persisted history of completed job runs (newest first),
	// populated only on the diagnostics page.
	RecentRuns []SchedulerRunView
}

type ScheduledJobView struct {
	ServerID            int64
	ServerName          string
	FlagEmoji           string
	CountryName         string
	JobType             string
	Status              string
	Detail              string
	LastError           string
	NextRunAt           string
	LastStartedAt       string
	LastSuccessAt       string
	LastDuration        string
	ConsecutiveFailures int
	Paused              bool
	ToggleURL           string
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
	Action                     string
	ConnectTimeout             string
	CommandTimeout             string
	StoredCredentialsAvailable bool
	RefreshURL                 string
	Errors                     map[string]string
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
	CPUModel             string
	CPUCores             string
	TotalRAM             string
	TotalDisk            string
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
	Action                     string
	ConnectTimeout             string
	CommandTimeout             string
	TrafficInterface           string
	StoredCredentialsAvailable bool
	RefreshURL                 string
	Errors                     map[string]string
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

// MonitoringLiveView powers the real-time metrics panel. Enabled is false when
// the server has no stored credentials or the live-metrics hub is unavailable,
// in which case the panel renders a short explanatory note instead of opening a
// WebSocket. IntervalSeconds is the live sampling cadence shown in the UI.
type MonitoringLiveView struct {
	Enabled         bool
	WSURL           string
	IntervalSeconds int
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
	ServerID       int64
	ServerName     string
	FlagEmoji      string
	CountryName    string
	CPUUsage       string
	RAMUsage       string
	DiskUsage      string
	LoadAverage    string
	UptimeHuman    string
	NetworkSummary string
	CollectedAt    string
	CurrentMonthDL string
	PeakBandwidth  string
}

// HomeWarningView is one dismissible warning banner on the home dashboard.
// ID is stable per condition (server + kind + occurrence) so a client-side
// dismissal keeps the banner hidden until the condition changes.
type HomeWarningView struct {
	ID          string
	Severity    string // "danger" or "warning"
	Icon        string // lucide icon name
	ServerID    int64
	ServerName  string
	Message     string
	ActionURL   string
	ActionLabel string
}

// FleetNodeStatusView powers the home dashboard's node-status glance: one row per
// server that has at least one discovered node. HasStopped flags whether any
// server currently has a stopped node, so the section can lead with a warning.
type FleetNodeStatusView struct {
	Servers    []ServerNodeStatusView
	HasStopped bool
}

// ServerNodeStatusView is one server's node-status summary on the dashboard.
// State is one of "running" (all nodes up), "stopped" (≥1 stopped), or "partial"
// (some node in an unknown/other state but none stopped).
type ServerNodeStatusView struct {
	ServerID    int64
	ServerName  string
	FlagEmoji   string
	CountryName string
	Total       int
	Running     int
	Stopped     int
	Other       int
	State       string
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
	Action                     string
	ConnectTimeout             string
	CommandTimeout             string
	StoredCredentialsAvailable bool
	RefreshURL                 string
	Errors                     map[string]string
}

// NodeSnapshotView renders one discovered node card.  Name carries the
// dynamic instance name (e.g. "node", "node2", "rebecca-node") and Actions
// the provider's management operations.
type NodeSnapshotView struct {
	Name           string
	NodeType       string
	TypeLabel      string
	InstallMode    string
	Version        string
	HealthStatus   string
	ActivePorts    []string
	XrayPorts      []string
	ServicePort    string
	APIPort        string
	Protocol       string
	DataDir        string
	Confidence     string
	Dependencies   []string
	Evidence       []string
	CollectedAt    string
	Actions        []NodeActionView
	ActionsEnabled bool
	// UptimeKnown is true once at least one persisted status observation exists
	// for this node; UptimePct is the 30-day running percentage and UptimeDetail
	// the "N/M checks" breakdown shown on hover.
	UptimeKnown  bool
	UptimePct    int
	UptimeDetail string
}

// NodeActionView is one management action button on a node card.
type NodeActionView struct {
	Key    string
	Label  string
	Icon   string
	Danger bool
}

// NodeInstallFormView powers the "Install PasarGuard node" form, including the
// pre-install port and connection-type configuration.
type NodeInstallFormView struct {
	Action      string
	NodeName    string
	ServicePort string
	APIPort     string
	Protocol    string // selected protocol: "rest" or "grpc"
	APIKey      string
	Enabled     bool
	Errors      map[string]string
}

// NodeInstallChannelView is one release channel offered in the Rebecca install
// UI. Enabled=false renders as a disabled "coming soon" option.
type NodeInstallChannelView struct {
	Key     string
	Enabled bool
}

// NodeRebeccaInstallFormView powers the "Install Rebecca node (dev/beta)" form.
// Rebecca's model is the inverse of PasarGuard's: the user supplies the
// certificate (from their Rebecca panel) plus the two ports, and nothing is
// read back. The certificate is intentionally never echoed back into the form.
type NodeRebeccaInstallFormView struct {
	Action      string
	NodeName    string
	ServicePort string
	APIPort     string
	Channel     string // selected channel: "dev"
	Channels    []NodeInstallChannelView
	Enabled     bool
	OpenInitial bool // reopen the modal after a validation error
	Errors      map[string]string
}

// NodeInstallView backs the live install job page.  While IsRunning the page
// auto-refreshes via RefreshURL; once completed, Info carries the values the
// PasarGuard panel needs to register the node.
type NodeInstallView struct {
	Available     bool
	JobID         string
	NodeName      string
	Status        string
	IsRunning     bool
	StartedAt     string
	FinishedAt    string
	Duration      string
	Output        string
	Error         string
	RefreshURL    string
	RefreshMillis int
	NodesURL      string
	Info          NodeRegistrationView
}

// NodeRegistrationView holds the panel registration values shown after a
// successful install. These values are kept in memory only.
type NodeRegistrationView struct {
	Available   bool
	NodeName    string
	NodeIP      string
	ServicePort string
	Protocol    string
	APIKey      string
	Certificate string
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

// ── Alerts ───────────────────────────────────────────────────────────────────

// AlertOptionView is a single <select> option, used for server, channel, and
// metric dropdowns on the alert forms.
type AlertOptionView struct {
	Value    string
	Label    string
	Selected bool
}

// AlertMetricOptionView is one metric choice on the rule form. Kind/Unit drive
// the metric-aware condition controls (comparator + numeric threshold for
// "threshold" metrics, a bounded day count for "days", nothing for "boolean");
// NeedsLimit/NeedsHistory surface the forecast availability gates next to the
// picker. The visible label is resolved in the template from the i18n catalog
// (alerts.metric.<value>) so the form is fully bilingual.
type AlertMetricOptionView struct {
	Value        string
	Kind         string
	Unit         string
	// DefaultThreshold seeds the threshold input when the operator switches to
	// this metric ("" for boolean metrics, which take no threshold).
	DefaultThreshold string
	NeedsLimit       bool
	NeedsHistory     bool
	Selected         bool
}

// AlertRuleView renders one row in the rules section of the alerts overview.
type AlertRuleView struct {
	ID               int64
	ServerLabel      string
	IsGlobal         bool
	Metric           string
	MetricLabel      string
	// ConditionKind mirrors alerts.MetricKind: "boolean" rules render an
	// "on detection" badge instead of the meaningless internal "≥ 1".
	ConditionKind    string
	ComparatorSymbol string
	ThresholdDisplay string
	ConsecutiveHits  int
	// StreakSummary is a human-readable "N/M" pending-streak label shown when
	// the rule is accumulating consecutive breaches but has not yet fired.
	// Empty when there is no active streak or when ConsecutiveHits == 1.
	StreakSummary string
	Cooldown      string
	Severity      string
	ChannelLabel  string
	Enabled       bool
	Note          string
	EditURL       string
	DeleteURL     string
}

// AlertChannelView renders one row in the channels section.
type AlertChannelView struct {
	ID          int64
	Kind        string
	Name        string
	ChatID      string
	HasTemplate bool
	Enabled     bool
	EditURL     string
	DeleteURL   string
	TestURL     string
}

// AlertSilenceView renders one active or scheduled silence.
type AlertSilenceView struct {
	ID          int64
	ServerLabel string
	Metric      string
	MetricLabel string
	Reason      string
	Expires     string
	Active      bool
	DeleteURL   string
}

// AlertEventView renders one row in the alert history section.
type AlertEventView struct {
	ServerLabel string
	// FlagEmoji is the event server's country flag (or "" when undetected);
	// CountryName is shown on hover. Mirrors the servers-list badge.
	FlagEmoji   string
	CountryName string
	Metric      string
	MetricLabel string
	Value       string
	Threshold   string
	Severity    string
	State       string
	FiredAt     string
	ResolvedAt  string
}

// AlertSilenceFormView powers the inline "mute a metric" form on the overview.
type AlertSilenceFormView struct {
	Action        string
	Reason        string
	ExpiresHours  string
	ServerOptions []AlertOptionView
	MetricOptions []AlertOptionView
	Errors        map[string]string
}

// AlertsOverviewView is the data backing the /alerts management page.
type AlertsOverviewView struct {
	Rules    []AlertRuleView
	Channels []AlertChannelView
	Silences []AlertSilenceView
	// Events holds one page of alert history; EventsTotal is the full count
	// and EventsPagination drives the page control under the table.
	Events           []AlertEventView
	EventsTotal      int
	EventsPagination PaginationView
	SilenceForm      AlertSilenceFormView
	HasServers       bool
	NewRuleURL       string
	NewChannelURL    string
	TokenConfigured  bool
	TokenNotice      string
}

// AlertRuleFormView powers the rule create/edit form.
type AlertRuleFormView struct {
	ID              int64
	Metric          string
	Comparator      string
	Threshold       string
	ConsecutiveHits string
	CooldownSeconds string
	Severity        string
	Enabled         bool
	Note            string
	Action          string
	DeleteAction    string
	ServerOptions   []AlertOptionView
	ChannelOptions  []AlertOptionView
	// Metrics lists the selectable metrics with the metadata the condition UI
	// needs; SelectedMetric echoes the currently chosen entry so the server
	// renders the right condition controls even without JavaScript.
	Metrics        []AlertMetricOptionView
	SelectedMetric AlertMetricOptionView
	// CooldownIsPreset is true when CooldownSeconds matches one of the form's
	// preset cooldown options; otherwise the template injects the stored custom
	// value as an extra option (labelled CooldownCustomLabel) so editing an
	// older rule never silently rewrites its cooldown.
	CooldownIsPreset    bool
	CooldownCustomLabel string
	Errors              map[string]string
}

// AlertChannelFormView powers the channel create/edit form.
type AlertChannelFormView struct {
	ID              int64
	Kind            string
	Name            string
	ChatID          string
	MessageTemplate string
	Enabled         bool
	Action          string
	DeleteAction    string
	TokenConfigured bool
	TokenNotice     string
	Errors          map[string]string
}

// ── Bulk actions ─────────────────────────────────────────────────────────────

// BulkActionResultView backs the content-bulk-result template.  Bulk actions
// run as background jobs; while InProgressCount > 0 the page auto-refreshes
// via RefreshURL until Finished.
type BulkActionResultView struct {
	Available bool
	Action    string
	// ActionLabel is the human-facing action name shown in the result header and
	// page copy (e.g. "node restart" instead of the raw "node-restart" key).
	ActionLabel     string
	Results         []BulkServerResultView
	OKCount         int
	FailedCount     int
	SkippedCount    int
	InProgressCount int
	Total           int
	Finished        bool
	RefreshURL      string
}

// BulkServerResultView is one row in the bulk result table.
type BulkServerResultView struct {
	ID       int64
	Name     string
	Status   string // "pending", "running", "ok", "failed", "skipped"
	ExitCode string
	Reason   string
}

// ── Interactive terminal ──────────────────────────────────────────────────────

// TerminalTargetView identifies the server the terminal connects to.
type TerminalTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	Username           string
	AuthMode           string
	CredentialStrategy string
	WSURL              string
	// InitCommand is an optional command auto-run once the shell connects
	// (e.g. an interactive command forwarded from the command center).
	InitCommand string
}

// TerminalFormView powers the credential-collection form.
type TerminalFormView struct {
	Action                     string
	ConnectTimeout             string
	Password                   string
	PrivateKey                 string
	KeyPassphrase              string
	StoredCredentialsAvailable bool
	// InitCommand carries the optional auto-run command across the credential
	// POST so it survives the form round-trip.
	InitCommand string
	Errors      map[string]string
}

// ── Analytics ────────────────────────────────────────────────────────────────

type AnalyticsTargetView struct {
	ID                 int64
	Name               string
	Host               string
	Port               int
	AuthMode           string
	Username           string
	Tags               []string
	CredentialStrategy string
}

type TopServerMetricView struct {
	ServerID   int64
	ServerName string
	// FlagEmoji is the server's country flag (or "" when undetected); CountryName
	// is shown on hover. Mirrors the servers-list badge so the overview stays
	// visually consistent.
	FlagEmoji   string
	CountryName string
	CPU         string
	RAM         string
	Disk        string
}

type TopServerTrafficView struct {
	ServerID   int64
	ServerName string
	// FlagEmoji is the server's country flag (or "" when undetected); CountryName
	// is shown on hover. Mirrors the servers-list badge so the overview stays
	// visually consistent.
	FlagEmoji   string
	CountryName string
	Download    string // current-month RX, human-readable
	Upload      string // current-month TX, human-readable
	MonthBytes  string // current-month total, human-readable
	MonthLabel  string
}

type GlobalAnalyticsView struct {
	ServerCount int
	TopMetrics  []TopServerMetricView
	TopTraffic  []TopServerTrafficView
	// Traffic-limit summary for the banner that links to /analytics/limits:
	// the current global default cap (if any) and how many per-tag caps exist.
	HasGlobalLimit   bool
	GlobalLimitHuman string
	TagLimitCount    int
}

// AnalyticsTrafficSummaryView is the current-month download/upload/total strip
// shown on a single server's analytics page. HasData is false when the server
// has no vnstat row for the current month.
type AnalyticsTrafficSummaryView struct {
	HasData    bool
	MonthLabel string
	Download   string
	Upload     string
	Total      string
}

// AnalyticsLimitView powers the per-server monthly download (RX) limit form on
// the analytics page. HasLimit reflects whether a cap is currently configured;
// ValueInput/UnitInput pre-fill the form (the stored byte count rendered back as
// a value + unit). LimitHuman is the human-readable current cap for display.
type AnalyticsLimitView struct {
	Action      string
	HasLimit    bool
	LimitHuman  string
	ValueInput  string
	UnitInput   string
	UnitOptions []string
	// KindInput is the selected limit kind ("rx"/"tx"/"total"); KindLabel is its
	// localized label shown next to the current cap.
	KindInput string
	KindLabel string
	Error     string
	// Inherited describes a group/global cap that applies when this server has no
	// per-server limit of its own. InheritedHuman is the effective cap and
	// InheritedSource is a human-readable origin ("global default" / tag "x").
	// Both are empty when no limit applies at any level.
	InheritedHuman  string
	InheritedSource string
}

// TrafficLimitsView powers the fleet-level group/global limits admin page. The
// global default is one optional cap; Tags lists the per-tag caps. Actions point
// at the three POST endpoints; UnitOptions/GlobalUnit mirror the per-server form.
type TrafficLimitsView struct {
	GlobalAction    string
	TagAction       string
	TagDeleteAction string
	HasGlobal       bool
	GlobalHuman     string
	GlobalValue     string
	GlobalUnit      string
	UnitOptions     []string
	Tags            []TrafficLimitTagView
	Error           string
}

// TrafficLimitTagView is one per-tag cap row on the limits admin page.
type TrafficLimitTagView struct {
	Tag        string
	LimitHuman string
}

type Renderer struct {
	templates *template.Template
	bundle    *i18n.Bundle
}

func NewRenderer() (*Renderer, error) {
	bundle, err := i18n.Default()
	if err != nil {
		return nil, err
	}

	funcMap := template.FuncMap{
		"trimSuffix": strings.TrimSuffix,
		"hasSuffix":  strings.HasSuffix,
		"float64": func(s string) float64 {
			var v float64
			fmt.Sscanf(s, "%f", &v)
			return v
		},
		// t/tn/tsafe are placeholders so the templates parse; Render rebinds them
		// to the request's active language by cloning the template set per render.
		"t":     func(key string, _ ...any) string { return key },
		"tn":    func(key string, _ int, _ ...any) string { return key },
		"tsafe": func(key string, _ ...any) template.HTML { return template.HTML(key) },
		// clientI18nJSON ships the client-needed strings to the browser; rebound
		// per render to the active language (placeholder emits an empty object).
		"clientI18nJSON": func() template.JS { return template.JS("{}") },
	}
	templates, err := template.New("").Funcs(funcMap).ParseFS(assets.Templates(), "web/templates/*.gohtml")
	if err != nil {
		return nil, err
	}

	return &Renderer{templates: templates, bundle: bundle}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, statusCode int, data PageData) error {
	// Ensure the page has an active localizer (default language for request
	// paths that never set one), then bind the {{ t }}/{{ tn }} funcs to it.
	if data.localizer == nil {
		data.SetLocalizer(r.bundle.Localizer(i18n.DefaultLanguage))
	}
	data = normalizePageData(data)

	contentName := strings.TrimSpace(data.ContentTemplate)
	if contentName == "" {
		return fmt.Errorf("view: content template name is required")
	}

	// Clone the parsed set per render and rebind the translation funcs to the
	// active language. Cloning is cheap relative to the work a request already
	// does, and it lets {{ t "key" }} resolve the right language anywhere in the
	// templates — including inside range blocks — without threading the dot.
	tmpl, err := r.templates.Clone()
	if err != nil {
		return fmt.Errorf("view: clone templates: %w", err)
	}
	loc := data.localizer
	tmpl.Funcs(template.FuncMap{
		"t":              loc.T,
		"tn":             loc.Tn,
		"tsafe":          func(key string, args ...any) template.HTML { return template.HTML(loc.Tsafe(key, args...)) },
		"clientI18nJSON": func() template.JS { return clientI18nJSON(loc) },
	})

	var content bytes.Buffer
	if err := tmpl.ExecuteTemplate(&content, contentName, data); err != nil {
		return fmt.Errorf("view: render %s: %w", contentName, err)
	}
	data.MainHTML = template.HTML(content.String())

	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, "layout", data); err != nil {
		return fmt.Errorf("view: render layout: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, err = buffer.WriteTo(w)
	return err
}

func normalizePageData(data PageData) PageData {
	if data.Lang == "" {
		data.Lang = i18n.DefaultLanguage
	}
	if data.Dir == "" {
		data.Dir = "ltr"
	}

	if data.Title == "" {
		data.Title = data.AppName
	}

	if data.Description == "" {
		// Localized fallback for request paths that build PageData without
		// NewPageData (which normally sets this); never an English literal.
		data.Description = data.T("shell.meta_description")
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
		data.FooterNote = data.T("shell.footer_note")
	}

	if len(data.NavigationItems) == 0 {
		data.NavigationItems = defaultNavigation(data.ActiveNav)
	}

	for index := range data.NavigationItems {
		data.NavigationItems[index].Active = data.NavigationItems[index].Href == data.ActiveNav
	}

	return data
}
