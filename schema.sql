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
  tag TEXT NOT NULL,
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
