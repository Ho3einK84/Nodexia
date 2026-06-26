CREATE TABLE IF NOT EXISTS servers (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22,
  auth_mode TEXT NOT NULL,
  username TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  credential_strategy TEXT NOT NULL DEFAULT 'runtime',
  credential_ref TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_tags (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  tag VARCHAR(64) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE,
  UNIQUE (server_id, tag)
);

CREATE INDEX IF NOT EXISTS idx_server_tags_server_id
  ON server_tags (server_id);

CREATE TABLE IF NOT EXISTS command_history (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  command_text TEXT NOT NULL,
  exit_code INTEGER,
  stdout TEXT NOT NULL DEFAULT '',
  stderr TEXT NOT NULL DEFAULT '',
  executed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_command_history_server_id
  ON command_history (server_id);

CREATE TABLE IF NOT EXISTS system_snapshots (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  cpu_usage REAL NOT NULL DEFAULT 0,
  ram_usage REAL NOT NULL DEFAULT 0,
  disk_usage REAL NOT NULL DEFAULT 0,
  load_average_1 REAL NOT NULL DEFAULT 0,
  load_average_5 REAL NOT NULL DEFAULT 0,
  load_average_15 REAL NOT NULL DEFAULT 0,
  uptime_seconds INTEGER NOT NULL DEFAULT 0,
  network_summary TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_system_snapshots_server_id
  ON system_snapshots (server_id);

CREATE TABLE IF NOT EXISTS vnstat_snapshots (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  available INTEGER NOT NULL DEFAULT 0,
  interface_name TEXT NOT NULL DEFAULT '',
  available_interfaces_json TEXT NOT NULL DEFAULT '[]',
  daily_rows_json TEXT NOT NULL DEFAULT '[]',
  monthly_rows_json TEXT NOT NULL DEFAULT '[]',
  peak_mbps REAL NOT NULL DEFAULT 0,
  avg_mbps REAL NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT '',
  collected_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_vnstat_snapshots_server_id
  ON vnstat_snapshots (server_id);

CREATE TABLE IF NOT EXISTS node_snapshots (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  node_type TEXT NOT NULL,
  service_name TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  health_status TEXT NOT NULL DEFAULT 'unknown',
  active_ports TEXT NOT NULL DEFAULT '',
  dependencies_json TEXT NOT NULL DEFAULT '[]',
  install_mode TEXT NOT NULL DEFAULT '',
  api_port TEXT NOT NULL DEFAULT '',
  confidence TEXT NOT NULL DEFAULT '',
  evidence_json TEXT NOT NULL DEFAULT '[]',
  xray_ports TEXT NOT NULL DEFAULT '',
  service_port TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_node_snapshots_server_id
  ON node_snapshots (server_id);

CREATE TABLE IF NOT EXISTS server_system_facts (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  hostname TEXT NOT NULL DEFAULT '',
  os_name TEXT NOT NULL DEFAULT '',
  os_version TEXT NOT NULL DEFAULT '',
  kernel_version TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  uptime_seconds INTEGER NOT NULL DEFAULT 0,
  last_update_unix INTEGER NOT NULL DEFAULT 0,
  collected_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_server_system_facts_server_id
  ON server_system_facts (server_id);

CREATE TABLE IF NOT EXISTS install_metadata (
  id INTEGER PRIMARY KEY,
  domain TEXT NOT NULL DEFAULT '',
  installed_at DATETIME,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ── Alerting ─────────────────────────────────────────────────────────────────
-- Channels describe where a notification is delivered. The Telegram bot token
-- is a SECRET and is intentionally NOT stored here: it is read from the
-- environment variable NODEXIA_TELEGRAM_BOT_TOKEN. Only the non-secret chat id
-- and an optional message template live in the database.
-- alert_channels is created before alert_rules so the channel foreign key has a
-- valid referent on engines (MySQL) that resolve foreign keys at creation time.
CREATE TABLE IF NOT EXISTS alert_channels (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT 'telegram',
  name TEXT NOT NULL,
  chat_id TEXT NOT NULL DEFAULT '',
  message_template TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_alert_channels_enabled
  ON alert_channels (enabled);

-- alert_rules bind a metric threshold to an optional server and channel.
-- server_id NULL means the rule applies to every server (a global rule);
-- channel_id NULL means dispatch to all enabled channels.
CREATE TABLE IF NOT EXISTS alert_rules (
  id INTEGER PRIMARY KEY,
  server_id INTEGER,
  metric VARCHAR(64) NOT NULL,
  comparator TEXT NOT NULL DEFAULT 'gte',
  threshold REAL NOT NULL,
  consecutive_hits INTEGER NOT NULL DEFAULT 1,
  cooldown_seconds INTEGER NOT NULL DEFAULT 900,
  severity TEXT NOT NULL DEFAULT 'warning',
  channel_id INTEGER,
  enabled INTEGER NOT NULL DEFAULT 1,
  note TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE,
  FOREIGN KEY (channel_id) REFERENCES alert_channels(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_server_id
  ON alert_rules (server_id);

CREATE INDEX IF NOT EXISTS idx_alert_rules_enabled
  ON alert_rules (enabled);

-- alert_silences mute a specific metric (or the literal `all`) for one server.
-- expires_at NULL means the silence stays until it is removed manually.
CREATE TABLE IF NOT EXISTS alert_silences (
  id INTEGER PRIMARY KEY,
  server_id INTEGER NOT NULL,
  metric VARCHAR(64) NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  expires_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE,
  UNIQUE (server_id, metric)
);

CREATE INDEX IF NOT EXISTS idx_alert_silences_server_id
  ON alert_silences (server_id);

-- alert_events record firing/resolved transitions (written in the evaluation
-- phase). They are the persistent source of truth for open alerts across
-- restarts.
CREATE TABLE IF NOT EXISTS alert_events (
  id INTEGER PRIMARY KEY,
  rule_id INTEGER,
  server_id INTEGER NOT NULL,
  metric VARCHAR(64) NOT NULL,
  observed_value REAL NOT NULL DEFAULT 0,
  threshold REAL NOT NULL DEFAULT 0,
  severity TEXT NOT NULL DEFAULT 'warning',
  state VARCHAR(32) NOT NULL DEFAULT 'firing',
  fired_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at DATETIME,
  notified_at DATETIME,
  FOREIGN KEY (rule_id) REFERENCES alert_rules(id) ON DELETE SET NULL,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_alert_events_server_id
  ON alert_events (server_id);

CREATE INDEX IF NOT EXISTS idx_alert_events_server_metric_state
  ON alert_events (server_id, metric, state);

-- alert_rule_streaks tracks the in-progress consecutive-breach count per
-- (rule, server) pair. Persisting this avoids streak loss on restart, which
-- previously caused rules with consecutive_hits > 1 to silently reset and
-- never reach the fire threshold when the app restarted between cycles.
CREATE TABLE IF NOT EXISTS alert_rule_streaks (
  rule_id   INTEGER NOT NULL,
  server_id INTEGER NOT NULL,
  streak    INTEGER NOT NULL DEFAULT 0,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (rule_id, server_id),
  FOREIGN KEY (rule_id)   REFERENCES alert_rules(id)  ON DELETE CASCADE,
  FOREIGN KEY (server_id) REFERENCES servers(id)      ON DELETE CASCADE
);

-- node_snapshots.data_dir records each discovered node's data directory
-- (e.g. /var/lib/<name>). Appended as a standalone statement so existing
-- databases pick it up as a new bootstrap migration.
ALTER TABLE node_snapshots ADD COLUMN data_dir TEXT NOT NULL DEFAULT '';

-- ── Analytics & Historical Metrics ────────────────────────────────────────────

-- system_snapshots.swap_usage tracks swap utilisation alongside RAM. Added as a
-- standalone migration so existing databases gain the column automatically.
ALTER TABLE system_snapshots ADD COLUMN swap_usage REAL NOT NULL DEFAULT 0;

-- metric_rollups_hourly holds pre-aggregated system metrics per server per hour.
-- Retention: 6 months. Raw data in system_snapshots is kept for 30 days, then
-- these hourly rollups become the authoritative time-series source.
CREATE TABLE IF NOT EXISTS metric_rollups_hourly (
  id           INTEGER PRIMARY KEY,
  server_id    INTEGER NOT NULL,
  period_start DATETIME NOT NULL,
  avg_cpu      REAL NOT NULL DEFAULT 0,
  avg_ram      REAL NOT NULL DEFAULT 0,
  avg_disk     REAL NOT NULL DEFAULT 0,
  avg_swap     REAL NOT NULL DEFAULT 0,
  avg_load1    REAL NOT NULL DEFAULT 0,
  avg_load5    REAL NOT NULL DEFAULT 0,
  avg_load15   REAL NOT NULL DEFAULT 0,
  sample_count INTEGER NOT NULL DEFAULT 0,
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE,
  UNIQUE (server_id, period_start)
);

CREATE INDEX IF NOT EXISTS idx_metric_rollups_hourly_server_period
  ON metric_rollups_hourly (server_id, period_start);

-- metric_rollups_daily holds pre-aggregated daily metrics.
-- Retention: 2 years.
CREATE TABLE IF NOT EXISTS metric_rollups_daily (
  id           INTEGER PRIMARY KEY,
  server_id    INTEGER NOT NULL,
  period_start DATETIME NOT NULL,
  avg_cpu      REAL NOT NULL DEFAULT 0,
  avg_ram      REAL NOT NULL DEFAULT 0,
  avg_disk     REAL NOT NULL DEFAULT 0,
  avg_swap     REAL NOT NULL DEFAULT 0,
  avg_load1    REAL NOT NULL DEFAULT 0,
  avg_load5    REAL NOT NULL DEFAULT 0,
  avg_load15   REAL NOT NULL DEFAULT 0,
  sample_count INTEGER NOT NULL DEFAULT 0,
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE,
  UNIQUE (server_id, period_start)
);

CREATE INDEX IF NOT EXISTS idx_metric_rollups_daily_server_period
  ON metric_rollups_daily (server_id, period_start);

-- ── Geo / country detection ───────────────────────────────────────────────────
-- Each server's detected public-IP country is cached on the row so it never has
-- to be looked up on a page render. Detection runs over the established SSH
-- connection to the node (see internal/geoip), so the code reflects the node's
-- own egress IP rather than the panel's network. Appended as standalone
-- statements so existing databases pick them up as new bootstrap migrations.
-- country_checked_at records the last resolution attempt (success OR empty) so
-- the scheduler can back off and refresh on a generous cadence instead of
-- hammering the rate-limited geo services.
ALTER TABLE servers ADD COLUMN country_code TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN country_name TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN country_checked_at DATETIME;

-- ── Per-server traffic limits ─────────────────────────────────────────────────
-- An OPTIONAL monthly DOWNLOAD (RX) traffic cap per server, used by the analytics
-- forecast to flag exhaustion risk and project days-to-limit. The limit is kept
-- in its own table (one row per server, server_id is the primary key) rather than
-- a column on servers so the feature stays self-contained and revertable, and the
-- core servers row is never touched by a forecasting concern. No row means "no
-- limit configured", which preserves today's behaviour for every existing server.
-- Semantics are RX-only on purpose: the whole forecast projects download (RX)
-- traffic, and the current-month value it compares against is the monthly RX from
-- vnstat — so the cap is measured against the exact metric the forecast predicts.
CREATE TABLE IF NOT EXISTS server_traffic_limits (
  server_id           INTEGER PRIMARY KEY,
  monthly_limit_bytes INTEGER NOT NULL,
  updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

-- ── System hardware facts ─────────────────────────────────────────────────────
-- CPU model/cores plus total RAM and root-filesystem size, collected by the
-- system module alongside the existing OS/kernel facts and shown on the server
-- System tab. Appended as standalone statements so existing databases pick them
-- up as new bootstrap migrations. cpu_cores/mem_total_kb/disk_total_kb are
-- integers (logical CPUs, and KiB as /proc/meminfo and `df -k` report); 0 means
-- "not collected". mem/disk are stored in KiB and humanised at render time.
ALTER TABLE server_system_facts ADD COLUMN cpu_model TEXT NOT NULL DEFAULT '';
ALTER TABLE server_system_facts ADD COLUMN cpu_cores INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_system_facts ADD COLUMN mem_total_kb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_system_facts ADD COLUMN disk_total_kb INTEGER NOT NULL DEFAULT 0;

-- ── Group / global traffic limits ─────────────────────────────────────────────
-- Broader monthly DOWNLOAD (RX) caps that apply when a server has no per-server
-- limit of its own (server_traffic_limits stays the authoritative override). Two
-- scopes share one table: scope='global' (ref '') is a single fleet-wide default,
-- and scope='tag' (ref = the tag name) caps every server carrying that tag. The
-- effective limit for a server is resolved as: its own server_traffic_limits row,
-- else the SMALLEST tag cap among its tags, else the global default — so the
-- broader caps never override an explicit per-server value, and a missing row at
-- every level means "unlimited" (today's behaviour for every existing server).
-- Appended as a standalone statement so existing databases pick it up as a new
-- bootstrap migration.
CREATE TABLE IF NOT EXISTS traffic_limit_rules (
  scope               VARCHAR(16)  NOT NULL,
  ref                 VARCHAR(64)  NOT NULL DEFAULT '',
  monthly_limit_bytes INTEGER      NOT NULL,
  updated_at          DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (scope, ref)
);
