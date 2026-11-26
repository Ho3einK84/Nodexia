/* Nodexia Termius-style intelligent command autocomplete.
 *
 * Combines:
 *  1. Per-server & global learned command history (localStorage + server DB sync)
 *  2. User snippets (synced from nodexia.terminal.customSnippets + defaults)
 *  3. Standard Ubuntu / Linux server administration command catalog (~170 entries)
 *  4. Dynamic service/path completion learned from command history
 *  5. Live xterm.js buffer + mobile IME composition state fusion
 *
 * UI adapts cleanly by viewport:
 *  - Desktop: cursor-anchored floating popup with match highlighting & inline
 *    ghost-text preview (Tab / ArrowRight at EOL accepts; Up/Down navigates;
 *    Esc dismisses; Ctrl+Space triggers manually).
 *  - Mobile / compact: horizontally scrollable thumb-friendly suggestion chip
 *    strip docked cleanly above the accessory toolbar (never dismisses keyboard).
 */
(function () {
  'use strict';

  function T(key, params) { return window.nxT ? window.nxT(key, params) : key; }

  /* ── Standard Ubuntu / Linux Command Catalog ──────────────── */
  // Ordered within each command group by everyday frequency so common actions
  // (e.g. "apt update", "apt upgrade", "apt install ") rank highest first.
  var UBUNTU_COMMANDS = [
    // apt / apt-get / dpkg / snap
    { cmd: 'apt update',                       desc: 'Update package index' },
    { cmd: 'apt upgrade',                      desc: 'Upgrade installed packages' },
    { cmd: 'apt upgrade -y',                   desc: 'Upgrade packages without prompt' },
    { cmd: 'apt update && apt upgrade -y',     desc: 'Refresh index & upgrade packages' },
    { cmd: 'apt full-upgrade',                 desc: 'Upgrade including kernel/deps' },
    { cmd: 'apt install ',                     desc: 'Install package(s)' },
    { cmd: 'apt remove ',                      desc: 'Remove installed package' },
    { cmd: 'apt purge ',                       desc: 'Remove package and config files' },
    { cmd: 'apt autoremove',                   desc: 'Remove unused dependencies' },
    { cmd: 'apt autoremove --purge -y',        desc: 'Purge unused dependencies' },
    { cmd: 'apt search ',                      desc: 'Search available packages' },
    { cmd: 'apt show ',                        desc: 'Show package details' },
    { cmd: 'apt list --installed',             desc: 'List installed packages' },
    { cmd: 'apt list --upgradable',            desc: 'List upgradable packages' },
    { cmd: 'apt policy ',                      desc: 'Show installed & candidate versions' },
    { cmd: 'apt clean',                        desc: 'Clear downloaded .deb cache' },
    { cmd: 'apt-get update',                   desc: 'Refresh package lists' },
    { cmd: 'apt-get upgrade -y',               desc: 'Upgrade packages (apt-get)' },
    { cmd: 'apt-get dist-upgrade -y',          desc: 'Full distribution upgrade' },
    { cmd: 'apt-get install -y ',              desc: 'Install package non-interactively' },
    { cmd: 'apt-cache policy ',                desc: 'Check package version policy' },
    { cmd: 'dpkg -l',                          desc: 'List installed .deb packages' },
    { cmd: 'dpkg -i ',                         desc: 'Install local .deb package' },
    { cmd: 'dpkg --configure -a',              desc: 'Fix interrupted package setup' },
    { cmd: 'snap list',                        desc: 'List installed snap packages' },
    { cmd: 'snap install ',                    desc: 'Install snap package' },
    { cmd: 'snap refresh',                     desc: 'Update installed snaps' },

    // systemd & journald
    { cmd: 'systemctl status ',                desc: 'Check service unit status' },
    { cmd: 'systemctl restart ',               desc: 'Restart service unit' },
    { cmd: 'systemctl start ',                 desc: 'Start service unit' },
    { cmd: 'systemctl stop ',                  desc: 'Stop service unit' },
    { cmd: 'systemctl reload ',                desc: 'Reload service configuration' },
    { cmd: 'systemctl enable ',                desc: 'Enable service on boot' },
    { cmd: 'systemctl enable --now ',          desc: 'Enable and start service' },
    { cmd: 'systemctl disable ',               desc: 'Disable service on boot' },
    { cmd: 'systemctl daemon-reload',          desc: 'Reload systemd unit files' },
    { cmd: 'systemctl list-units --type=service', desc: 'List active services' },
    { cmd: 'systemctl list-units --failed',    desc: 'Show failed systemd units' },
    { cmd: 'systemctl is-active ',             desc: 'Check if service is active' },
    { cmd: 'journalctl -u ',                   desc: 'View logs for a unit' },
    { cmd: 'journalctl -f',                    desc: 'Follow live system logs' },
    { cmd: 'journalctl -n 100 --no-pager',     desc: 'Show last 100 log entries' },
    { cmd: 'journalctl -xe',                   desc: 'Jump to recent errors & context' },
    { cmd: 'journalctl -p 3 -xb --no-pager',   desc: 'Show boot error-level logs' },
    { cmd: 'journalctl --since "1 hour ago"',  desc: 'Logs from the last hour' },
    { cmd: 'journalctl --vacuum-time=7d',      desc: 'Prune logs older than 7 days' },
    { cmd: 'journalctl --disk-usage',          desc: 'Show journal disk footprint' },

    // Docker & Compose
    { cmd: 'docker ps',                        desc: 'List running containers' },
    { cmd: 'docker ps -a',                     desc: 'List all containers' },
    { cmd: 'docker logs -f ',                  desc: 'Follow container logs' },
    { cmd: 'docker logs --tail 100 ',          desc: 'Last 100 lines of container log' },
    { cmd: 'docker exec -it ',                 desc: 'Run interactive command in container' },
    { cmd: 'docker images',                    desc: 'List local container images' },
    { cmd: 'docker stats',                     desc: 'Live container CPU/RAM usage' },
    { cmd: 'docker inspect ',                  desc: 'Inspect container/image metadata' },
    { cmd: 'docker restart ',                  desc: 'Restart container' },
    { cmd: 'docker stop ',                     desc: 'Stop running container' },
    { cmd: 'docker rm -f ',                    desc: 'Force remove container' },
    { cmd: 'docker system df',                 desc: 'Show Docker disk usage' },
    { cmd: 'docker system prune -f',           desc: 'Clean unused Docker resources' },
    { cmd: 'docker compose up -d',             desc: 'Start compose stack detached' },
    { cmd: 'docker compose up -d --build',     desc: 'Rebuild and start compose stack' },
    { cmd: 'docker compose down',              desc: 'Stop and remove compose stack' },
    { cmd: 'docker compose ps',                desc: 'List compose stack services' },
    { cmd: 'docker compose logs -f',           desc: 'Follow compose service logs' },
    { cmd: 'docker compose restart',           desc: 'Restart compose services' },
    { cmd: 'docker compose pull',              desc: 'Pull updated compose images' },

    // Networking & Firewall
    { cmd: 'ip a',                             desc: 'Show IP addresses & interfaces' },
    { cmd: 'ip -c a',                          desc: 'Colored IP address listing' },
    { cmd: 'ip r',                             desc: 'Show IPv4 routing table' },
    { cmd: 'ip -6 r',                          desc: 'Show IPv6 routing table' },
    { cmd: 'ip -s link',                       desc: 'Interface packet/drop counters' },
    { cmd: 'ss -tulpn',                        desc: 'Listening TCP/UDP sockets & PIDs' },
    { cmd: 'ss -tunap',                        desc: 'All TCP/UDP sockets & processes' },
    { cmd: 'ss -s',                            desc: 'Socket usage summary' },
    { cmd: 'ufw status',                       desc: 'Show UFW firewall status' },
    { cmd: 'ufw status numbered',              desc: 'List numbered UFW rules' },
    { cmd: 'ufw allow ',                       desc: 'Allow port/service in firewall' },
    { cmd: 'ufw deny ',                        desc: 'Block port/service in firewall' },
    { cmd: 'ufw delete ',                      desc: 'Delete UFW rule by number' },
    { cmd: 'ufw enable',                       desc: 'Enable UFW firewall' },
    { cmd: 'ufw reload',                       desc: 'Reload UFW firewall rules' },
    { cmd: 'iptables -L -n -v',                desc: 'List filter iptables rules' },
    { cmd: 'iptables -t nat -L -n -v',         desc: 'List NAT iptables rules' },
    { cmd: 'curl -I ',                         desc: 'Fetch HTTP response headers' },
    { cmd: 'curl -sSL ',                       desc: 'Follow redirects silently' },
    { cmd: 'curl -s https://api.ipify.org && echo', desc: 'Show public IPv4 address' },
    { cmd: 'curl -s https://api64.ipify.org && echo', desc: 'Show public IPv6 address' },
    { cmd: 'wget -qO- ',                       desc: 'Fetch URL content to stdout' },
    { cmd: 'dig +short ',                      desc: 'Compact DNS lookup' },
    { cmd: 'ping -c 4 ',                       desc: 'Send 4 ICMP echo requests' },
    { cmd: 'mtr -rw ',                         desc: 'Run network path report' },
    { cmd: 'tcpdump -i any -nn port ',         desc: 'Capture packets on port' },

    // Files, Archives & Disk
    { cmd: 'ls -la',                           desc: 'List all files with permissions' },
    { cmd: 'ls -lh',                           desc: 'List files with human sizes' },
    { cmd: 'ls -lht',                          desc: 'List newest files first' },
    { cmd: 'cd /etc/',                         desc: 'Go to system config directory' },
    { cmd: 'cd /var/log/',                     desc: 'Go to system log directory' },
    { cmd: 'cd /opt/',                         desc: 'Go to /opt applications directory' },
    { cmd: 'mkdir -p ',                        desc: 'Create nested directories' },
    { cmd: 'rm -rf ',                          desc: 'Remove files/dirs recursively' },
    { cmd: 'cp -a ',                           desc: 'Copy preserving permissions' },
    { cmd: 'mv ',                              desc: 'Move or rename file/dir' },
    { cmd: 'chmod +x ',                        desc: 'Add executable permission' },
    { cmd: 'chmod 600 ',                       desc: 'Owner read/write only' },
    { cmd: 'chmod 644 ',                       desc: 'Standard file permissions' },
    { cmd: 'chown -R ',                        desc: 'Change owner recursively' },
    { cmd: 'ln -s ',                           desc: 'Create symbolic link' },
    { cmd: 'find . -name ',                    desc: 'Search files by name pattern' },
    { cmd: 'find / -type f -size +100M',       desc: 'Locate files over 100 MB' },
    { cmd: 'grep -rn ',                        desc: 'Recursive line-numbered search' },
    { cmd: 'tail -f ',                         desc: 'Follow file updates live' },
    { cmd: 'tail -n 100 ',                     desc: 'Show last 100 lines of file' },
    { cmd: 'head -n 50 ',                      desc: 'Show first 50 lines of file' },
    { cmd: 'cat ',                             desc: 'Print file contents' },
    { cmd: 'less ',                            desc: 'Page through file contents' },
    { cmd: 'tar -xzvf ',                       desc: 'Extract .tar.gz archive' },
    { cmd: 'tar -czvf ',                       desc: 'Create .tar.gz archive' },
    { cmd: 'unzip ',                           desc: 'Extract .zip archive' },
    { cmd: 'df -h',                            desc: 'Show mounted disk usage' },
    { cmd: 'du -sh *',                         desc: 'Size of items in current dir' },
    { cmd: 'du -h --max-depth=1',              desc: 'Subdirectory sizes (depth 1)' },
    { cmd: 'lsblk -f',                         desc: 'List block devices & filesystems' },

    // System, Processes & Hardware
    { cmd: 'htop',                             desc: 'Interactive process monitor' },
    { cmd: 'top',                              desc: 'Live process activity monitor' },
    { cmd: 'free -h',                          desc: 'Show memory & swap usage' },
    { cmd: 'uptime',                           desc: 'System uptime & load averages' },
    { cmd: 'uname -a',                         desc: 'Kernel version & architecture' },
    { cmd: 'cat /etc/os-release',              desc: 'Ubuntu/Linux OS details' },
    { cmd: 'lsb_release -a',                   desc: 'Distribution release info' },
    { cmd: 'lscpu',                            desc: 'CPU model & core layout' },
    { cmd: 'ps aux',                           desc: 'List all running processes' },
    { cmd: 'ps aux --sort=-%cpu | head -n 15', desc: 'Top 15 CPU-consuming processes' },
    { cmd: 'ps aux --sort=-%mem | head -n 15', desc: 'Top 15 RAM-consuming processes' },
    { cmd: 'lsof -i :',                        desc: 'Processes listening on port' },
    { cmd: 'kill -9 ',                         desc: 'Force kill process by PID' },
    { cmd: 'pkill -f ',                        desc: 'Kill processes matching pattern' },
    { cmd: 'dmesg -T | tail -n 30',            desc: 'Recent kernel ring messages' },
    { cmd: 'timedatectl status',               desc: 'Clock, timezone & NTP sync' },
    { cmd: 'hostnamectl',                      desc: 'Hostname & OS metadata' },
    { cmd: 'who',                              desc: 'Currently logged-in users' },
    { cmd: 'last -n 20',                       desc: 'Recent login sessions' },
    { cmd: 'crontab -l',                       desc: 'List scheduled cron jobs' },
    { cmd: 'crontab -e',                       desc: 'Edit user crontab' },
    { cmd: 'reboot',                           desc: 'Reboot host immediately' },
    { cmd: 'shutdown -h now',                  desc: 'Power off host immediately' },

    // Web, TLS, Security, SSH, Git, Editors & Tmux
    { cmd: 'nginx -t',                         desc: 'Test Nginx config syntax' },
    { cmd: 'nginx -s reload',                  desc: 'Reload Nginx configuration' },
    { cmd: 'caddy validate',                   desc: 'Validate Caddyfile syntax' },
    { cmd: 'caddy reload',                     desc: 'Reload Caddy configuration' },
    { cmd: 'certbot certificates',             desc: 'List SSL/TLS certificates' },
    { cmd: 'certbot renew --dry-run',          desc: 'Test Let\'s Encrypt renewal' },
    { cmd: 'fail2ban-client status',           desc: 'Show active Fail2ban jails' },
    { cmd: 'fail2ban-client status sshd',      desc: 'Check banned SSH IPs' },
    { cmd: 'rsync -avz --progress ',           desc: 'Sync files with compression' },
    { cmd: 'ssh-keygen -t ed25519',            desc: 'Generate Ed25519 SSH keypair' },
    { cmd: 'git status',                       desc: 'Show git working tree status' },
    { cmd: 'git pull',                         desc: 'Fetch & merge remote branch' },
    { cmd: 'git log --oneline -n 20',          desc: 'Recent 20 commits summary' },
    { cmd: 'git diff',                         desc: 'Inspect unstaged changes' },
    { cmd: 'git branch -a',                    desc: 'List local & remote branches' },
    { cmd: 'git checkout ',                    desc: 'Switch branch or restore files' },
    { cmd: 'tmux ls',                          desc: 'List active tmux sessions' },
    { cmd: 'tmux attach -t ',                  desc: 'Attach to existing tmux session' },
    { cmd: 'tmux new -s ',                     desc: 'Start named tmux session' },
    { cmd: 'nano ',                            desc: 'Edit file with nano' },
    { cmd: 'vim ',                             desc: 'Edit file with vim' },
    { cmd: 'passwd',                           desc: 'Change account password' },
    { cmd: 'adduser ',                         desc: 'Create new Ubuntu user' },
    { cmd: 'usermod -aG sudo ',                desc: 'Add user to sudo group' },
    { cmd: 'visudo',                           desc: 'Edit /etc/sudoers safely' },
    { cmd: 'clear',                            desc: 'Clear terminal screen' },
    { cmd: 'history | tail -n 30',             desc: 'Show last 30 shell commands' },
    { cmd: 'exit',                             desc: 'Close current shell session' }
  ];

  // Quick subcommand completions when user types a command + space
  var SUBCOMMAND_TEMPLATES = {
    'apt':         ['update', 'upgrade', 'upgrade -y', 'install ', 'remove ', 'purge ', 'autoremove', 'search ', 'show ', 'list --installed', 'list --upgradable', 'clean', 'full-upgrade'],
    'apt-get':     ['update', 'upgrade -y', 'dist-upgrade -y', 'install -y ', 'remove ', 'autoremove -y', 'clean'],
    'systemctl':   ['status ', 'restart ', 'start ', 'stop ', 'reload ', 'enable ', 'enable --now ', 'disable ', 'daemon-reload', 'list-units --type=service', 'list-units --failed', 'is-active '],
    'journalctl':  ['-u ', '-f', '-xe', '-n 100 --no-pager', '-p 3 -xb --no-pager', '--since "1 hour ago"', '--vacuum-time=7d', '--disk-usage'],
    'docker':      ['ps', 'ps -a', 'logs -f ', 'logs --tail 100 ', 'exec -it ', 'images', 'stats', 'inspect ', 'restart ', 'stop ', 'rm -f ', 'compose up -d', 'compose down', 'compose logs -f', 'compose ps', 'system df', 'system prune -f'],
    'docker compose': ['up -d', 'up -d --build', 'down', 'ps', 'logs -f', 'restart', 'pull', 'build'],
    'ufw':         ['status', 'status numbered', 'allow ', 'deny ', 'delete ', 'enable', 'disable', 'reload'],
    'git':         ['status', 'pull', 'push', 'log --oneline -n 20', 'diff', 'branch -a', 'checkout ', 'switch ', 'add -A', 'commit -m ""'],
    'tmux':        ['ls', 'attach -t ', 'new -s ', 'kill-session -t '],
    'ip':          ['a', '-c a', 'r', '-6 r', '-s link'],
    'ss':          ['-tulpn', '-tunap', '-s'],
    'nginx':       ['-t', '-s reload', '-T'],
    'caddy':       ['validate', 'reload', 'version'],
    'certbot':     ['certificates', 'renew --dry-run']
  };

  var COMMON_UBUNTU_UNITS = [
    'ssh', 'nginx', 'caddy', 'docker', 'ufw', 'fail2ban',
    'cron', 'postgresql', 'mysql', 'redis-server', 'systemd-resolved'
  ];

  var STORAGE_PREFIX = 'nodexia.terminal.history.v1.';
  var ENABLED_KEY = 'nodexia.terminal.autocomplete';
  var SNIPPETS_KEY = 'nodexia.terminal.customSnippets';
  var MAX_SERVER_HISTORY = 300;
  var MAX_GLOBAL_HISTORY = 500;

  var PASSWORD_PROMPT_RE = /(?:password|passphrase|pin|verification code|otp|کلمه عبور|رمز)(?:\s+for\s+[^:]+)?\s*:?\s*$/i;
  var SHELL_PROMPT_RE = /^((?:[^\s]*@[^\s]+(?::[^\n#$%>❯➜]*)?|\([^\)]+\)\s*[^\n#$%>❯➜]*|[┌└│─\s\w.\-@:\/~\[\]\(\)]*?)[#$%❯➜]\s)/;

  /* ── Prompt & Screen Line Extraction ──────────────────────── */
  function readLogicalLineAroundCursor(term) {
    if (!term || !term.buffer || !term.buffer.active) return null;
    var buf = term.buffer.active;
    if (buf.type === 'alternate') {
      return { active: false, reason: 'alternate' };
    }
    var curRow = (buf.baseY || 0) + (buf.cursorY || 0);
    var curCol = buf.cursorX || 0;

    var startRow = curRow;
    while (startRow > 0) {
      var prevLine = buf.getLine(startRow);
      if (!prevLine || !prevLine.isWrapped) break;
      startRow -= 1;
    }

    var endRow = curRow;
    while (endRow + 1 < buf.length) {
      var nextLine = buf.getLine(endRow + 1);
      if (!nextLine || !nextLine.isWrapped) break;
      endRow += 1;
    }

    var beforeCursor = '';
    for (var r = startRow; r < curRow; r++) {
      var ln = buf.getLine(r);
      beforeCursor += ln ? ln.translateToString(false) : '';
    }
    var curLineObj = buf.getLine(curRow);
    var curRowStr = curLineObj ? curLineObj.translateToString(false) : '';
    beforeCursor += curRowStr.slice(0, curCol);

    var afterCursor = curRowStr.slice(curCol);
    for (var r2 = curRow + 1; r2 <= endRow; r2++) {
      var ln2 = buf.getLine(r2);
      afterCursor += ln2 ? ln2.translateToString(false) : '';
    }
    afterCursor = afterCursor.replace(/\s+$/, '');

    return {
      active: true,
      startRow: startRow,
      curRow: curRow,
      curCol: curCol,
      beforeCursor: beforeCursor,
      afterCursor: afterCursor
    };
  }

  function extractPromptFromLine(beforeCursor, anchorPrefix) {
    if (!beforeCursor) return { matched: false, prompt: '', typed: '' };

    if (PASSWORD_PROMPT_RE.test(beforeCursor)) {
      return { matched: false, isPassword: true, prompt: beforeCursor, typed: '' };
    }

    if (typeof anchorPrefix === 'string' && anchorPrefix.length > 0 && beforeCursor.indexOf(anchorPrefix) === 0) {
      return {
        matched: true,
        prompt: anchorPrefix,
        typed: beforeCursor.slice(anchorPrefix.length)
      };
    }

    var m = beforeCursor.match(SHELL_PROMPT_RE);
    if (m && m[1]) {
      var prompt = m[1];
      if (PASSWORD_PROMPT_RE.test(prompt)) {
        return { matched: false, isPassword: true, prompt: prompt, typed: '' };
      }
      return {
        matched: true,
        prompt: prompt,
        typed: beforeCursor.slice(prompt.length)
      };
    }

    return { matched: false, prompt: '', typed: '' };
  }

  /* ── Command Chain / Prefix Splitter ──────────────────────── */
  function splitCommandChain(line) {
    var inSingle = false;
    var inDouble = false;
    var escaped = false;
    var splitAt = 0;

    for (var i = 0; i < line.length; i++) {
      var ch = line.charAt(i);
      if (escaped) {
        escaped = false;
        continue;
      }
      if (ch === '\\' && !inSingle) {
        escaped = true;
        continue;
      }
      if (ch === "'" && !inDouble) {
        inSingle = !inSingle;
        continue;
      }
      if (ch === '"' && !inSingle) {
        inDouble = !inDouble;
        continue;
      }
      if (inSingle || inDouble) continue;

      if (ch === ';' || ch === '|') {
        if (ch === '|' && line.charAt(i + 1) === '|') {
          i++;
        }
        splitAt = i + 1;
      } else if (ch === '&' && line.charAt(i + 1) === '&') {
        i++;
        splitAt = i + 1;
      }
    }

    var rawBefore = line.slice(0, splitAt);
    var rest = line.slice(splitAt);
    var leadSpaces = rest.match(/^\s*/)[0] || '';
    return {
      chainPrefix: rawBefore + leadSpaces,
      segment: rest.slice(leadSpaces.length)
    };
  }

  function splitSudoPrefix(segment) {
    var m = segment.match(/^((?:sudo|doas)\s+)/);
    if (m) {
      return { sudoPrefix: m[1], inner: segment.slice(m[1].length) };
    }
    return { sudoPrefix: '', inner: segment };
  }

  function sanitizeCommandForStorage(raw) {
    if (!raw || typeof raw !== 'string') return '';
    if (raw.charAt(0) === ' ' || raw.charAt(0) === '\t') return '';
    if (/[\r\n\x00\x1b]/.test(raw)) return '';
    var trimmed = raw.trim();
    if (!trimmed || trimmed.length > 512) return '';
    if (trimmed.length < 2 && trimmed !== 'w') return '';
    var lower = trimmed.toLowerCase();
    var secretHints = [
      'password=', 'passwd=', 'passphrase=', 'token=', 'secret=',
      'api_key=', 'apikey=', '--password', '--token', '--secret',
      'authorization:', 'bearer '
    ];
    for (var i = 0; i < secretHints.length; i++) {
      if (lower.indexOf(secretHints[i]) !== -1) return '';
    }
    return trimmed;
  }

  /* ── Ranking & Query Engine ───────────────────────────────── */
  function createEngine(options) {
    options = options || {};
    var serverHistory = [];
    var globalHistory = [];
    var snippets = Array.isArray(options.snippets) ? options.snippets.slice() : [];

    // Learned dynamic contexts from history
    var learnedUnits = []; // e.g. ['nodexia', 'rebecca', 'nginx']
    var learnedPaths = []; // e.g. ['/root/agy-remote', '/opt/rebecca/.env']

    var ubuntuIndex = UBUNTU_COMMANDS.map(function (item, idx) {
      return {
        cmd: item.cmd,
        lower: item.cmd.toLowerCase(),
        desc: item.desc,
        source: 'ubuntu',
        priority: UBUNTU_COMMANDS.length - idx
      };
    });

    function rebuildLearnedContexts() {
      var unitSet = Object.create(null);
      var pathSet = Object.create(null);

      function scan(list) {
        for (var i = 0; i < list.length; i++) {
          var cmd = list[i].cmd;
          if (!cmd) continue;
          var unitMatch = cmd.match(/(?:systemctl\s+(?:status|restart|start|stop|reload|enable|disable|is-active)|journalctl\s+-u)\s+([A-Za-z0-9_.@-]+)/);
          if (unitMatch && unitMatch[1] && unitMatch[1].charAt(0) !== '-') {
            unitSet[unitMatch[1]] = (unitSet[unitMatch[1]] || 0) + (list[i].count || 1);
          }
          var tokens = cmd.split(/\s+/);
          for (var t = 0; t < tokens.length; t++) {
            var tok = tokens[t];
            if (tok.length >= 4 && (tok.charAt(0) === '/' || tok.indexOf('./') === 0 || tok.indexOf('~/') === 0)) {
              pathSet[tok] = (pathSet[tok] || 0) + (list[i].count || 1);
            }
          }
        }
      }

      scan(serverHistory);
      scan(globalHistory);

      learnedUnits = Object.keys(unitSet).sort(function (a, b) {
        return unitSet[b] - unitSet[a];
      }).slice(0, 30);

      learnedPaths = Object.keys(pathSet).sort(function (a, b) {
        return pathSet[b] - pathSet[a];
      }).slice(0, 40);
    }

    function setHistory(srvList, globList) {
      serverHistory = normalizeList(srvList, MAX_SERVER_HISTORY);
      globalHistory = normalizeList(globList, MAX_GLOBAL_HISTORY);
      rebuildLearnedContexts();
    }

    function setSnippets(list) {
      snippets = Array.isArray(list) ? list.slice() : [];
    }

    function normalizeList(list, max) {
      if (!Array.isArray(list)) return [];
      var map = Object.create(null);
      for (var i = 0; i < list.length; i++) {
        var entry = list[i];
        var rawCmd = typeof entry === 'string' ? entry : (entry && entry.cmd);
        var clean = sanitizeCommandForStorage(rawCmd);
        if (!clean) continue;
        var count = (entry && typeof entry.count === 'number' && entry.count > 0) ? entry.count : 1;
        var ts = (entry && typeof entry.ts === 'number' && entry.ts > 0) ? entry.ts : 0;
        if (map[clean]) {
          map[clean].count = Math.max(map[clean].count, count);
          map[clean].ts = Math.max(map[clean].ts, ts);
        } else {
          map[clean] = { cmd: clean, count: count, ts: ts };
        }
      }
      return Object.keys(map).map(function (k) { return map[k]; }).sort(function (a, b) {
        if (b.ts !== a.ts) return b.ts - a.ts;
        return b.count - a.count;
      }).slice(0, max);
    }

    function mergeHistory(srvList, globList) {
      serverHistory = mergeLists(serverHistory, srvList, MAX_SERVER_HISTORY);
      globalHistory = mergeLists(globalHistory, globList, MAX_GLOBAL_HISTORY);
      rebuildLearnedContexts();
    }

    function mergeLists(existing, incoming, max) {
      var combined = [];
      if (Array.isArray(existing)) combined = combined.concat(existing);
      if (Array.isArray(incoming)) combined = combined.concat(incoming);
      return normalizeList(combined, max);
    }

    function addCommand(raw) {
      var clean = sanitizeCommandForStorage(raw);
      if (!clean) return false;
      var now = Date.now();
      var found = false;
      for (var i = 0; i < serverHistory.length; i++) {
        if (serverHistory[i].cmd === clean) {
          serverHistory[i].count = (serverHistory[i].count || 1) + 1;
          serverHistory[i].ts = now;
          found = true;
          break;
        }
      }
      if (!found) {
        serverHistory.unshift({ cmd: clean, count: 1, ts: now });
        if (serverHistory.length > MAX_SERVER_HISTORY) serverHistory.length = MAX_SERVER_HISTORY;
      } else {
        serverHistory.sort(function (a, b) {
          if (b.ts !== a.ts) return b.ts - a.ts;
          return b.count - a.count;
        });
      }
      rebuildLearnedContexts();
      return true;
    }

    function clearServerHistory() {
      serverHistory = [];
      rebuildLearnedContexts();
    }

    function getServerHistory() {
      return serverHistory.slice();
    }

    function scoreCandidate(candCmd, query, baseScore, now) {
      if (!query) return 0;
      var qLower = query.toLowerCase();
      var cLower = candCmd.toLowerCase();
      if (candCmd === query) return 0;

      // 1. Exact case-sensitive prefix match
      if (candCmd.indexOf(query) === 0) {
        return baseScore + 300;
      }
      // 2. Case-insensitive prefix match
      if (cLower.indexOf(qLower) === 0) {
        return baseScore + 240;
      }
      // 3. Multi-token ordered prefix match (e.g. "apt u" -> "apt update")
      var qTokens = qLower.trim().split(/\s+/);
      if (qTokens.length >= 2) {
        var cTokens = cLower.trim().split(/\s+/);
        if (cTokens.length >= qTokens.length) {
          var qi = 0;
          for (var ci = 0; ci < cTokens.length && qi < qTokens.length; ci++) {
            if (cTokens[ci].indexOf(qTokens[qi]) === 0) {
              qi++;
            }
          }
          if (qi === qTokens.length) {
            return baseScore + 150;
          }
        }
      }
      // 4. Substring match for >= 3 char queries
      if (qLower.length >= 3) {
        var idx = cLower.indexOf(qLower);
        if (idx !== -1) {
          // Bonus if match starts on a word/path boundary
          var boundary = idx === 0 || /[\s\/\-_.]/.test(cLower.charAt(idx - 1));
          return baseScore + (boundary ? 85 : 35);
        }
      }
      return 0;
    }

    function recencyBonus(ts, now) {
      if (!ts) return 0;
      var ageHours = Math.max(0, (now - ts) / 3600000);
      if (ageHours < 1) return 120;
      if (ageHours < 24) return 90;
      if (ageHours < 168) return 55;
      return 20;
    }

    function query(lineBeforeCursor, queryOpts) {
      queryOpts = queryOpts || {};
      var limit = queryOpts.limit || 8;
      var force = !!queryOpts.force;

      if (typeof lineBeforeCursor !== 'string') return [];
      var trimmedStart = lineBeforeCursor.replace(/^\s+/, '');
      if (!trimmedStart && !force) return [];

      var now = Date.now();
      var resultsByCmd = Object.create(null);

      function addResult(fullCmd, score, source, desc, count) {
        if (!fullCmd || fullCmd === lineBeforeCursor) return;
        // Never suggest a command that doesn't add/change anything meaningful
        var existing = resultsByCmd[fullCmd];
        if (!existing) {
          resultsByCmd[fullCmd] = {
            cmd: fullCmd,
            score: score,
            source: source,
            desc: desc || '',
            count: count || 0
          };
        } else {
          if (score > existing.score) {
            existing.score = score;
            existing.source = source;
          }
          if (!existing.desc && desc) existing.desc = desc;
          if (count > existing.count) existing.count = count;
        }
      }

      // If user explicitly triggered Ctrl+Space on an empty prompt, show recent history + top Ubuntu commands
      if (!trimmedStart && force) {
        for (var i = 0; i < Math.min(serverHistory.length, 4); i++) {
          var h = serverHistory[i];
          addResult(h.cmd, 1200 + recencyBonus(h.ts, now), 'history', '', h.count);
        }
        for (var j = 0; j < Math.min(ubuntuIndex.length, limit); j++) {
          addResult(ubuntuIndex[j].cmd, 500 + ubuntuIndex[j].priority, 'ubuntu', ubuntuIndex[j].desc, 0);
        }
        return finalizeResults(resultsByCmd, limit);
      }

      var chain = splitCommandChain(lineBeforeCursor);
      var segment = chain.segment;
      var sudoInfo = splitSudoPrefix(segment);
      var inner = sudoInfo.inner;
      var fullPrefix = chain.chainPrefix + sudoInfo.sudoPrefix;

      // A. Match against server history (both full line and current segment/inner)
      for (var s = 0; s < serverHistory.length; s++) {
        var sh = serverHistory[s];
        var boost = recencyBonus(sh.ts, now) + Math.min(80, (sh.count || 1) * 12);
        var scFull = scoreCandidate(sh.cmd, lineBeforeCursor, 900 + boost, now);
        if (scFull > 0) {
          addResult(sh.cmd, scFull, 'history', '', sh.count);
        }
        if (fullPrefix && inner) {
          var scSeg = scoreCandidate(sh.cmd, inner, 820 + boost, now);
          if (scSeg > 0) {
            addResult(fullPrefix + sh.cmd, scSeg, 'history', '', sh.count);
          }
        }
      }

      // B. Match against global history (commands learned on other servers)
      for (var g = 0; g < globalHistory.length; g++) {
        var gh = globalHistory[g];
        var gBoost = recencyBonus(gh.ts, now) + Math.min(45, (gh.count || 1) * 6);
        var gFull = scoreCandidate(gh.cmd, lineBeforeCursor, 700 + gBoost, now);
        if (gFull > 0) {
          addResult(gh.cmd, gFull, 'history', '', gh.count);
        }
        if (fullPrefix && inner) {
          var gSeg = scoreCandidate(gh.cmd, inner, 650 + gBoost, now);
          if (gSeg > 0) {
            addResult(fullPrefix + gh.cmd, gSeg, 'history', '', gh.count);
          }
        }
      }

      // C. Match against saved & default snippets
      for (var sn = 0; sn < snippets.length; sn++) {
        var snip = snippets[sn];
        if (!snip || !snip.cmd) continue;
        var snipTarget = inner || segment;
        var scSnip = scoreCandidate(snip.cmd, snipTarget, 620, now);
        if (scSnip > 0) {
          addResult(fullPrefix + snip.cmd, scSnip, 'snippet', snip.name || snip.cat || '', 0);
        }
      }

      // D. Dynamic learned unit / path completion (e.g. "systemctl status " -> learned units)
      if (inner) {
        var svcMatch = inner.match(/^(systemctl\s+(?:status|restart|start|stop|reload|enable|disable|is-active)\s+|journalctl\s+-u\s+)(\S*)$/);
        if (svcMatch) {
          var svcBase = svcMatch[1];
          var svcQuery = svcMatch[2] || '';
          for (var u = 0; u < learnedUnits.length; u++) {
            var unit = learnedUnits[u];
            if (!svcQuery || unit.toLowerCase().indexOf(svcQuery.toLowerCase()) === 0) {
              addResult(fullPrefix + svcBase + unit, 760 - u, 'history', 'unit', 1);
            }
          }
          for (var cu = 0; cu < COMMON_UBUNTU_UNITS.length; cu++) {
            var cUnit = COMMON_UBUNTU_UNITS[cu];
            if (!svcQuery || cUnit.toLowerCase().indexOf(svcQuery.toLowerCase()) === 0) {
              addResult(fullPrefix + svcBase + cUnit, 620 - cu, 'ubuntu', 'service', 0);
            }
          }
        }

        // Dynamic path completion from history when typing a path argument (e.g. "cat /root/agy")
        var pathArgMatch = inner.match(/^(.+\s+)((?:\/|\.\/|~\/)[^\s]*)$/);
        if (pathArgMatch) {
          var cmdHead = pathArgMatch[1];
          var pathPart = pathArgMatch[2];
          for (var p = 0; p < learnedPaths.length; p++) {
            var lp = learnedPaths[p];
            if (lp !== pathPart && lp.indexOf(pathPart) === 0) {
              addResult(fullPrefix + cmdHead + lp, 750 - p, 'history', 'path', 1);
            }
          }
        }
      }

      // E. Match against Standard Ubuntu Command Catalog + Subcommand Templates
      var catalogQuery = inner || segment;
      if (catalogQuery) {
        for (var c = 0; c < ubuntuIndex.length; c++) {
          var item = ubuntuIndex[c];
          var scCat = scoreCandidate(item.cmd, catalogQuery, 500 + Math.round(item.priority * 0.4), now);
          if (scCat > 0) {
            addResult(fullPrefix + item.cmd, scCat, 'ubuntu', item.desc, 0);
          }
        }

        // Subcommand expansion when user typed e.g. "apt " or "docker compose "
        var subKeys = Object.keys(SUBCOMMAND_TEMPLATES);
        for (var sk = 0; sk < subKeys.length; sk++) {
          var base = subKeys[sk];
          var prefixWithSpace = base + ' ';
          if (catalogQuery.toLowerCase().indexOf(prefixWithSpace) === 0) {
            var restArg = catalogQuery.slice(prefixWithSpace.length);
            var subs = SUBCOMMAND_TEMPLATES[base];
            for (var si = 0; si < subs.length; si++) {
              var fullSub = base + ' ' + subs[si];
              if (!restArg || subs[si].toLowerCase().indexOf(restArg.toLowerCase()) === 0) {
                addResult(fullPrefix + fullSub, 690 - si * 2, 'ubuntu', base + ' subcommand', 0);
              }
            }
          }
        }
      }

      return finalizeResults(resultsByCmd, limit);
    }

    function finalizeResults(resultsByCmd, limit) {
      var list = Object.keys(resultsByCmd).map(function (k) { return resultsByCmd[k]; });
      list.sort(function (a, b) {
        if (b.score !== a.score) return b.score - a.score;
        return a.cmd.length - b.cmd.length;
      });
      if (list.length > limit) list.length = limit;
      return list;
    }

    return {
      setHistory: setHistory,
      mergeHistory: mergeHistory,
      setSnippets: setSnippets,
      addCommand: addCommand,
      clearServerHistory: clearServerHistory,
      getServerHistory: getServerHistory,
      query: query
    };
  }

  /* ── LocalStorage Persistence Helper ──────────────────────── */
  function readStoredList(key) {
    try {
      var raw = window.localStorage.getItem(key);
      if (!raw) return [];
      var parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed : [];
    } catch (e) {
      return [];
    }
  }

  function writeStoredList(key, list) {
    try {
      window.localStorage.setItem(key, JSON.stringify(list || []));
    } catch (e) { /* quota or privacy mode */ }
  }

  function loadEnabledPref() {
    try {
      var v = window.localStorage.getItem(ENABLED_KEY);
      if (v === 'false') return false;
    } catch (e) {}
    return true;
  }

  function saveEnabledPref(enabled) {
    try {
      window.localStorage.setItem(ENABLED_KEY, enabled ? 'true' : 'false');
    } catch (e) {}
  }

  function loadSnippetsForAutocomplete(defaultSnippets) {
    var customs = [];
    try {
      var raw = window.localStorage.getItem(SNIPPETS_KEY);
      if (raw) {
        var parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) customs = parsed;
      }
    } catch (e) {}
    return customs.concat(Array.isArray(defaultSnippets) ? defaultSnippets : []);
  }

  /* ── Live UI + Terminal Controller ────────────────────────── */
  function attach(opts) {
    var term = opts.term;
    var card = opts.card;
    var container = opts.container;
    var serverID = String(opts.serverID || '0');
    var historyURL = opts.historyURL || '';
    var getCsrf = typeof opts.getCsrf === 'function' ? opts.getCsrf : function () { return opts.csrfToken || ''; };
    var sendRawInput = opts.sendRawInput;
    var getInputController = typeof opts.getInputController === 'function' ? opts.getInputController : function () { return null; };
    var onLayoutChange = typeof opts.onLayoutChange === 'function' ? opts.onLayoutChange : function () {};
    var onToast = typeof opts.onToast === 'function' ? opts.onToast : function () {};
    var defaultSnippets = opts.defaultSnippets || [];

    var serverKey = STORAGE_PREFIX + 's' + serverID;
    var globalKey = STORAGE_PREFIX + 'global';

    var enabled = loadEnabledPref();
    var engine = createEngine({ snippets: loadSnippetsForAutocomplete(defaultSnippets) });
    engine.setHistory(readStoredList(serverKey), readStoredList(globalKey));

    // DOM elements: inline ghost text + desktop floating popup + mobile chip strip
    var ghostEl = document.createElement('span');
    ghostEl.className = 'terminal-ac-ghost';
    ghostEl.setAttribute('aria-hidden', 'true');
    ghostEl.hidden = true;
    container.appendChild(ghostEl);

    var popupEl = document.createElement('div');
    popupEl.className = 'terminal-ac-popup';
    popupEl.setAttribute('role', 'listbox');
    popupEl.setAttribute('aria-label', T('js.terminal.ac_label'));
    popupEl.hidden = true;
    container.appendChild(popupEl);

    var stripEl = card.querySelector('#terminal-ac-strip');
    var stripListEl = stripEl ? stripEl.querySelector('#terminal-ac-strip-list') : null;
    var stripDismissBtn = stripEl ? stripEl.querySelector('#terminal-ac-strip-dismiss') : null;

    var anchorPrefix = null;
    var awaitingPromptSettle = true;
    var composingText = '';
    var currentLineState = null; // { prompt, ptyTyped, effectiveLine, hasAfterCursor }
    var currentItems = [];
    var selectedIdx = 0;
    var userNavigated = false;
    var dismissedQuery = null;
    var suppressUntilEdit = false;
    var lastEditAt = 0;
    var pendingDeletes = 0;
    var pendingAppend = '';
    var lastScreenTyped = '';
    var echoCheckTimer = null;
    var echoSuppressed = false;
    var sentEchoBytesSinceOutput = 0;
    var sentAtTypedLen = 0;
    var refreshTimer = null;
    var disposed = false;

    function isCompactViewport() {
      if (card && card.classList.contains('terminal-card--mobile')) return true;
      if (window.innerWidth <= 760) return true;
      if (window.innerHeight <= 480 && window.matchMedia && window.matchMedia('(pointer: coarse)').matches) return true;
      return false;
    }

    function setStripShown(show) {
      if (!stripEl) return;
      var wasShown = !stripEl.hidden;
      stripEl.hidden = !show;
      stripEl.style.display = show ? 'flex' : 'none';
      if (wasShown !== show) {
        requestAnimationFrame(onLayoutChange);
      }
    }

    function setPopupShown(show) {
      popupEl.hidden = !show;
      popupEl.style.display = show ? 'flex' : 'none';
    }

    function hideUI() {
      currentItems = [];
      selectedIdx = 0;
      userNavigated = false;
      ghostEl.hidden = true;
      ghostEl.style.display = 'none';
      ghostEl.textContent = '';
      setPopupShown(false);
      setStripShown(false);
    }

    setPopupShown(false);
    setStripShown(false);

    if (stripDismissBtn) {
      stripDismissBtn.addEventListener('mousedown', function (e) { e.preventDefault(); });
      stripDismissBtn.addEventListener('click', function (e) {
        e.preventDefault();
        dismissedQuery = currentLineState ? currentLineState.effectiveLine : '';
        hideUI();
        term.focus();
      });
    }

    function getCellMetrics() {
      var screenEl = container.querySelector('.xterm-screen');
      var containerRect = container.getBoundingClientRect();
      var screenRect = screenEl ? screenEl.getBoundingClientRect() : containerRect;
      var cols = (term && term.cols) || 80;
      var rows = (term && term.rows) || 24;
      var cellW = screenRect.width ? (screenRect.width / cols) : 8;
      var cellH = screenRect.height ? (screenRect.height / rows) : 16;
      var offsetX = screenRect.left - containerRect.left;
      var offsetY = screenRect.top - containerRect.top;
      return {
        containerWidth: containerRect.width,
        containerHeight: containerRect.height,
        offsetX: offsetX,
        offsetY: offsetY,
        cellW: cellW,
        cellH: cellH,
        cols: cols,
        rows: rows
      };
    }

    function renderGhost(topItem, state, metrics) {
      if (!topItem || !state || state.hasAfterCursor || !state.effectiveLine) {
        ghostEl.hidden = true;
        ghostEl.style.display = 'none';
        return;
      }
      var eff = state.effectiveLine;
      if (topItem.cmd.indexOf(eff) !== 0) {
        ghostEl.hidden = true;
        ghostEl.style.display = 'none';
        return;
      }
      var suffix = topItem.cmd.slice(eff.length);
      if (!suffix) {
        ghostEl.hidden = true;
        ghostEl.style.display = 'none';
        return;
      }

      var buf = term.buffer.active;
      var cx = buf.cursorX || 0;
      var cy = buf.cursorY || 0;
      var compOffset = 0;
      if (composingText) {
        var compView = container.querySelector('.composition-view.active');
        if (compView) {
          compOffset = compView.getBoundingClientRect().width;
        } else {
          compOffset = composingText.length * metrics.cellW;
        }
      }

      var leftPx = metrics.offsetX + cx * metrics.cellW + compOffset;
      var topPx = metrics.offsetY + cy * metrics.cellH;
      var availW = Math.max(0, metrics.containerWidth - leftPx - 8);
      if (availW < metrics.cellW * 2) {
        ghostEl.hidden = true;
        ghostEl.style.display = 'none';
        return;
      }

      ghostEl.textContent = suffix;
      ghostEl.style.left = Math.round(leftPx) + 'px';
      ghostEl.style.top = Math.round(topPx) + 'px';
      ghostEl.style.height = Math.round(metrics.cellH) + 'px';
      ghostEl.style.lineHeight = Math.round(metrics.cellH) + 'px';
      ghostEl.style.maxWidth = Math.round(availW) + 'px';
      if (term.options) {
        if (term.options.fontFamily) ghostEl.style.fontFamily = term.options.fontFamily;
        if (term.options.fontSize) ghostEl.style.fontSize = term.options.fontSize + 'px';
      }
      ghostEl.hidden = false;
      ghostEl.style.display = 'block';
    }

    function buildHighlightedCmd(cmd, query) {
      var span = document.createElement('span');
      span.className = 'terminal-ac-item__cmd';
      if (!query) {
        span.textContent = cmd;
        return span;
      }
      var qLower = query.toLowerCase();
      var cLower = cmd.toLowerCase();
      var idx = cLower.indexOf(qLower);
      if (idx !== -1) {
        if (idx > 0) span.appendChild(document.createTextNode(cmd.slice(0, idx)));
        var mark = document.createElement('mark');
        mark.className = 'terminal-ac-match';
        mark.textContent = cmd.slice(idx, idx + query.length);
        span.appendChild(mark);
        if (idx + query.length < cmd.length) {
          span.appendChild(document.createTextNode(cmd.slice(idx + query.length)));
        }
        return span;
      }
      var tokens = qLower.trim().split(/\s+/).filter(Boolean);
      if (tokens.length >= 2) {
        var cursor = 0;
        var matchedAny = false;
        for (var ti = 0; ti < tokens.length; ti++) {
          var tok = tokens[ti];
          var pos = cLower.indexOf(tok, cursor);
          if (pos === -1) break;
          if (pos > cursor) {
            span.appendChild(document.createTextNode(cmd.slice(cursor, pos)));
          }
          var tMark = document.createElement('mark');
          tMark.className = 'terminal-ac-match';
          tMark.textContent = cmd.slice(pos, pos + tok.length);
          span.appendChild(tMark);
          cursor = pos + tok.length;
          matchedAny = true;
        }
        if (matchedAny) {
          if (cursor < cmd.length) {
            span.appendChild(document.createTextNode(cmd.slice(cursor)));
          }
          return span;
        }
      }
      span.textContent = cmd;
      return span;
    }

    function sourceBadgeLabel(src) {
      if (src === 'history') return T('js.terminal.ac_history');
      if (src === 'snippet') return T('js.terminal.ac_snippet');
      return T('js.terminal.ac_ubuntu');
    }

    function renderDesktopPopup(items, query, metrics) {
      popupEl.innerHTML = '';
      var listEl = document.createElement('div');
      listEl.className = 'terminal-ac-popup__list';

      items.forEach(function (item, idx) {
        var row = document.createElement('button');
        row.type = 'button';
        row.className = 'terminal-ac-item' + (idx === selectedIdx ? ' is-selected' : '');
        row.setAttribute('role', 'option');
        row.setAttribute('aria-selected', idx === selectedIdx ? 'true' : 'false');

        var badge = document.createElement('span');
        badge.className = 'terminal-ac-badge terminal-ac-badge--' + item.source;
        badge.textContent = sourceBadgeLabel(item.source);

        var cmdEl = buildHighlightedCmd(item.cmd, query);

        row.appendChild(badge);
        row.appendChild(cmdEl);

        if (item.desc || item.count > 1) {
          var meta = document.createElement('span');
          meta.className = 'terminal-ac-item__meta';
          meta.textContent = item.desc || (item.count + '×');
          row.appendChild(meta);
        }

        row.addEventListener('mousedown', function (e) {
          e.preventDefault();
        });
        row.addEventListener('click', function (e) {
          e.preventDefault();
          acceptSuggestion(item);
        });
        listEl.appendChild(row);
      });

      popupEl.appendChild(listEl);

      var footer = document.createElement('div');
      footer.className = 'terminal-ac-popup__footer';
      [
        T('js.terminal.ac_hint_tab'),
        T('js.terminal.ac_hint_nav'),
        T('js.terminal.ac_hint_esc')
      ].forEach(function (hint) {
        var span = document.createElement('span');
        span.className = 'terminal-ac-hint';
        span.textContent = hint;
        footer.appendChild(span);
      });
      popupEl.appendChild(footer);

      setPopupShown(true);

      // Position near cursor, flipping above if near bottom edge
      var buf = term.buffer.active;
      var cursorLeft = metrics.offsetX + (buf.cursorX || 0) * metrics.cellW;
      var cursorTop = metrics.offsetY + (buf.cursorY || 0) * metrics.cellH;

      var popupW = Math.min(460, Math.max(220, metrics.containerWidth - 16));
      popupEl.style.width = popupW + 'px';
      var leftPx = Math.max(8, Math.min(cursorLeft, metrics.containerWidth - popupW - 8));
      popupEl.style.left = Math.round(leftPx) + 'px';

      var belowSpace = metrics.containerHeight - (cursorTop + metrics.cellH + 4) - 6;
      var aboveSpace = cursorTop - 6;
      var estPopupH = Math.min(268, items.length * 34 + 34);
      var preferBelow = belowSpace >= estPopupH || belowSpace >= aboveSpace;
      var availSpace = preferBelow ? belowSpace : aboveSpace;
      listEl.style.maxHeight = Math.round(Math.min(238, Math.max(76, availSpace - 34))) + 'px';

      if (preferBelow) {
        popupEl.style.top = Math.round(cursorTop + metrics.cellH + 4) + 'px';
      } else {
        var popupH = popupEl.offsetHeight || Math.min(220, availSpace);
        popupEl.style.top = Math.round(Math.max(6, cursorTop - popupH - 4)) + 'px';
      }

      var selectedRow = listEl.querySelector('.terminal-ac-item.is-selected');
      if (selectedRow && typeof selectedRow.scrollIntoView === 'function') {
        selectedRow.scrollIntoView({ block: 'nearest' });
      }
    }

    function renderMobileStrip(items) {
      if (!stripListEl) return;
      stripListEl.innerHTML = '';

      items.forEach(function (item, idx) {
        var chip = document.createElement('button');
        chip.type = 'button';
        chip.tabIndex = -1;
        chip.className = 'terminal-ac-chip' + (idx === 0 ? ' is-primary' : '');
        chip.setAttribute('data-ac-source', item.source);

        var dot = document.createElement('span');
        dot.className = 'terminal-ac-chip__dot terminal-ac-chip__dot--' + item.source;
        dot.setAttribute('aria-hidden', 'true');

        var label = document.createElement('span');
        label.className = 'terminal-ac-chip__text';
        label.textContent = item.cmd;

        chip.appendChild(dot);
        chip.appendChild(label);

        chip.addEventListener('mousedown', function (e) {
          e.preventDefault();
        });
        chip.addEventListener('click', function (e) {
          e.preventDefault();
          acceptSuggestion(item);
        });
        stripListEl.appendChild(chip);
      });

      setStripShown(true);
    }

    function renderActiveSuggestions(forceQuery) {
      if (disposed || !enabled) {
        hideUI();
        return;
      }
      if (suppressUntilEdit && !forceQuery) {
        hideUI();
        return;
      }
      if (echoSuppressed) {
        hideUI();
        return;
      }

      var raw = readLogicalLineAroundCursor(term);
      if (!raw || !raw.active) {
        hideUI();
        return;
      }

      var parsed = extractPromptFromLine(raw.beforeCursor, anchorPrefix);
      if (!parsed.matched || parsed.isPassword) {
        hideUI();
        return;
      }

      var ptyTyped = applyPendingEdit(parsed.typed);
      var effectiveLine = ptyTyped + (composingText || '');
      var hasAfterCursor = raw.afterCursor.length > 0;

      currentLineState = {
        prompt: parsed.prompt,
        ptyTyped: ptyTyped,
        effectiveLine: effectiveLine,
        hasAfterCursor: hasAfterCursor
      };

      if (hasAfterCursor && !forceQuery) {
        hideUI();
        return;
      }

      if (!forceQuery && dismissedQuery !== null && dismissedQuery === effectiveLine) {
        hideUI();
        return;
      }
      if (dismissedQuery !== null && effectiveLine !== dismissedQuery) {
        dismissedQuery = null;
      }

      var trimmedStart = effectiveLine.replace(/^\s+/, '');
      if (!trimmedStart && !forceQuery) {
        hideUI();
        return;
      }

      var compact = isCompactViewport();
      var items = engine.query(trimmedStart, {
        limit: compact ? 10 : 7,
        force: !!forceQuery
      });

      if (!items.length) {
        hideUI();
        return;
      }

      currentItems = items;
      if (selectedIdx >= currentItems.length) selectedIdx = 0;

      var metrics = getCellMetrics();
      renderGhost(currentItems[selectedIdx] || currentItems[0], currentLineState, metrics);

      if (compact) {
        setPopupShown(false);
        renderMobileStrip(currentItems);
      } else {
        setStripShown(false);
        renderDesktopPopup(currentItems, trimmedStart, metrics);
      }
    }

    function scheduleRefresh(forceQuery) {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(function () {
        refreshTimer = null;
        renderActiveSuggestions(forceQuery);
      }, 12);
    }

    function acceptSuggestion(item) {
      if (!item || !item.cmd) return;
      var state = currentLineState;
      if (!state) {
        var raw = readLogicalLineAroundCursor(term);
        if (raw && raw.active) {
          var p = extractPromptFromLine(raw.beforeCursor, anchorPrefix);
          if (p.matched) {
            state = { ptyTyped: p.typed, effectiveLine: p.typed + composingText };
          }
        }
      }

      var ptyTyped = state ? state.ptyTyped : '';
      var screenPtyTyped = ptyTyped;
      var rawNow = readLogicalLineAroundCursor(term);
      if (rawNow && rawNow.active) {
        var pNow = extractPromptFromLine(rawNow.beforeCursor, anchorPrefix);
        if (pNow.matched) {
          screenPtyTyped = pNow.typed;
        }
      }

      var ic = getInputController();
      if (ic && typeof ic.cancelComposition === 'function') {
        ic.cancelComposition();
      }
      composingText = '';

      var target = item.cmd;
      var toSend = '';
      if (target.indexOf(ptyTyped) === 0) {
        toSend = target.slice(ptyTyped.length);
        if (toSend) {
          pendingAppend += toSend;
        }
      } else {
        var bsCount = Array.from(ptyTyped).length;
        toSend = (bsCount > 0 ? new Array(bsCount + 1).join('\x7f') : '') + target;
        pendingDeletes = Array.from(screenPtyTyped).length;
        pendingAppend = target;
      }

      dismissedQuery = target;
      suppressUntilEdit = false;
      userNavigated = false;
      selectedIdx = 0;
      lastEditAt = Date.now();
      hideUI();

      if (toSend) {
        sendRawInput(toSend);
      }
      try { term.focus(); } catch (e) {}
    }

    function applyPendingEdit(screenTyped) {
      var s = screenTyped || '';
      if (pendingDeletes > 0) {
        var chars = Array.from(s);
        s = chars.slice(0, Math.max(0, chars.length - pendingDeletes)).join('');
      }
      return s + pendingAppend;
    }

    function trackPendingEdit(data) {
      if (!data) return;
      if (data === '\x7f' || data === '\x08') {
        if (pendingAppend.length > 0) {
          var aChars = Array.from(pendingAppend);
          aChars.pop();
          pendingAppend = aChars.join('');
        } else {
          pendingDeletes += 1;
        }
      } else if (data === '\x17') {
        if (pendingAppend.length > 0) {
          pendingAppend = pendingAppend.replace(/\S+\s*$/, '');
        } else {
          pendingDeletes = 0;
          pendingAppend = '';
        }
      } else if (data.charAt(0) !== '\x1b' && !/[\x00-\x1f]/.test(data)) {
        pendingAppend += data;
      }
    }

    function recordSubmittedCommand(rawCmd) {
      var clean = sanitizeCommandForStorage(rawCmd);
      if (!clean) return;
      engine.addCommand(clean);
      writeStoredList(serverKey, engine.getServerHistory());

      // Also persist to global localStorage pool
      var globals = readStoredList(globalKey);
      var updated = false;
      var now = Date.now();
      for (var i = 0; i < globals.length; i++) {
        if (globals[i] && globals[i].cmd === clean) {
          globals[i].count = (globals[i].count || 1) + 1;
          globals[i].ts = now;
          updated = true;
          break;
        }
      }
      if (!updated) globals.unshift({ cmd: clean, count: 1, ts: now });
      if (globals.length > MAX_GLOBAL_HISTORY) globals.length = MAX_GLOBAL_HISTORY;
      writeStoredList(globalKey, globals);

      if (historyURL) {
        syncCommandToServer(clean);
      }
    }

    function fetchWithCsrfRetry(url, fetchOpts) {
      var token = getCsrf();
      var headers = Object.assign({}, fetchOpts.headers || {});
      if (token) headers['X-CSRF-Token'] = token;
      var optsWithHeaders = Object.assign({}, fetchOpts, {
        headers: headers,
        credentials: 'same-origin'
      });

      return fetch(url, optsWithHeaders).then(function (resp) {
        if (resp.status !== 403) return resp;
        return fetch('/api/csrf-token', { credentials: 'same-origin' }).then(function (r) {
          return r.ok ? r.json() : null;
        }).then(function (d) {
          if (d && d.csrf_token) {
            card.setAttribute('data-csrf', d.csrf_token);
            var retryHeaders = Object.assign({}, fetchOpts.headers || {}, {
              'X-CSRF-Token': d.csrf_token
            });
            return fetch(url, Object.assign({}, fetchOpts, {
              headers: retryHeaders,
              credentials: 'same-origin'
            }));
          }
          return resp;
        }).catch(function () { return resp; });
      });
    }

    function syncCommandToServer(cmd) {
      if (!historyURL) return;
      fetchWithCsrfRetry(historyURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd })
      }).catch(function () { /* localStorage already updated */ });
    }

    function loadServerHistory() {
      if (!historyURL) return;
      fetch(historyURL, { credentials: 'same-origin' })
        .then(function (resp) { return resp.ok ? resp.json() : null; })
        .then(function (data) {
          if (disposed || !data || !data.ok) return;
          engine.mergeHistory(data.server_history || [], data.global_history || []);
          writeStoredList(serverKey, engine.getServerHistory());
          if (currentItems.length || (currentLineState && currentLineState.effectiveLine)) {
            scheduleRefresh(false);
          }
        })
        .catch(function () {});
    }

    function clearHistory() {
      if (!historyURL) {
        engine.clearServerHistory();
        writeStoredList(serverKey, []);
        hideUI();
        onToast(T('js.terminal.ac_history_cleared'), 2500, false);
        return;
      }

      fetchWithCsrfRetry(historyURL, { method: 'DELETE' })
        .then(function (resp) {
          if (!resp || !resp.ok) {
            onToast(T('js.failed'), 3000, true);
            return;
          }
          engine.clearServerHistory();
          writeStoredList(serverKey, []);
          hideUI();
          onToast(T('js.terminal.ac_history_cleared'), 2500, false);
        })
        .catch(function () {
          onToast(T('js.failed'), 3000, true);
        });
    }

    function onUserInput(data) {
      if (!data || disposed) return;
      // Ignore terminal internal responses (e.g. DSR cursor report \x1b[...R, focus \x1b[I)
      if (/^\x1b\[[0-9;?]*[ROIJ]$/.test(data)) return;

      // Handle Enter / Ctrl+C / Ctrl+D -> commit command to history & reset prompt anchor
      if (data === '\r' || data === '\n') {
        if (!echoSuppressed && !PASSWORD_PROMPT_RE.test((currentLineState && currentLineState.prompt) || '')) {
          var raw = readLogicalLineAroundCursor(term);
          if (raw && raw.active) {
            var parsed = extractPromptFromLine(raw.beforeCursor, anchorPrefix);
            if (parsed.matched && !parsed.isPassword) {
              var fullCmd = applyPendingEdit(parsed.typed) + (composingText || '') + raw.afterCursor;
              recordSubmittedCommand(fullCmd);
            }
          }
        }
        anchorPrefix = null;
        awaitingPromptSettle = true;
        dismissedQuery = null;
        suppressUntilEdit = false;
        userNavigated = false;
        selectedIdx = 0;
        lastEditAt = 0;
        pendingDeletes = 0;
        pendingAppend = '';
        lastScreenTyped = '';
        sentEchoBytesSinceOutput = 0;
        echoSuppressed = false;
        hideUI();
        return;
      }

      if (data === '\x03' || data === '\x04' || data === '\x15') {
        if (data === '\x15' && anchorPrefix !== null) {
          // Ctrl+U clears the line but keeps the same prompt
          dismissedQuery = null;
          suppressUntilEdit = false;
          userNavigated = false;
          selectedIdx = 0;
          lastEditAt = Date.now();
          pendingDeletes = 0;
          pendingAppend = '';
          scheduleRefresh(false);
          return;
        }
        anchorPrefix = null;
        awaitingPromptSettle = true;
        dismissedQuery = null;
        suppressUntilEdit = false;
        userNavigated = false;
        selectedIdx = 0;
        lastEditAt = 0;
        pendingDeletes = 0;
        pendingAppend = '';
        lastScreenTyped = '';
        sentEchoBytesSinceOutput = 0;
        echoSuppressed = false;
        hideUI();
        return;
      }

      // ArrowUp / ArrowDown in bash history when popup is closed: suppress popup until user edits
      if ((data === '\x1b[A' || data === '\x1b[B') && popupEl.hidden) {
        suppressUntilEdit = true;
        lastEditAt = 0;
        pendingDeletes = 0;
        pendingAppend = '';
        hideUI();
        return;
      }

      suppressUntilEdit = false;
      userNavigated = false;
      selectedIdx = 0;
      lastEditAt = Date.now();

      // Capture exact end-of-prompt column right before first printable char echoes
      if (awaitingPromptSettle) {
        var preLine = readLogicalLineAroundCursor(term);
        if (preLine && preLine.active && !PASSWORD_PROMPT_RE.test(preLine.beforeCursor)) {
          var fallbackParsed = extractPromptFromLine(preLine.beforeCursor, null);
          if (fallbackParsed.matched) {
            anchorPrefix = fallbackParsed.prompt;
            lastScreenTyped = fallbackParsed.typed;
          } else if (preLine.beforeCursor.length > 0 && /[\s#$%>❯➜]$/.test(preLine.beforeCursor)) {
            anchorPrefix = preLine.beforeCursor;
            lastScreenTyped = '';
          }
        }
        awaitingPromptSettle = false;
      }

      trackPendingEdit(data);

      // Track potential hidden-echo password entry
      if (data !== '\x7f' && data !== '\x08' && data.charAt(0) !== '\x1b') {
        var cur = readLogicalLineAroundCursor(term);
        if (cur && cur.active) {
          var p = extractPromptFromLine(cur.beforeCursor, anchorPrefix);
          sentAtTypedLen = p.matched ? p.typed.length : cur.beforeCursor.length;
          sentEchoBytesSinceOutput += data.length;
          if (echoCheckTimer) clearTimeout(echoCheckTimer);
          echoCheckTimer = setTimeout(function () {
            if (sentEchoBytesSinceOutput >= 3) {
              var after = readLogicalLineAroundCursor(term);
              if (after && after.active) {
                var pAfter = extractPromptFromLine(after.beforeCursor, anchorPrefix);
                var afterLen = pAfter.matched ? pAfter.typed.length : after.beforeCursor.length;
                if (afterLen <= sentAtTypedLen) {
                  echoSuppressed = true;
                  hideUI();
                }
              }
            }
          }, 800);
        }
      }

      scheduleRefresh(false);
    }

    function onCompositionChange(text) {
      composingText = text || '';
      if (composingText) {
        suppressUntilEdit = false;
        userNavigated = false;
        selectedIdx = 0;
        lastEditAt = Date.now();
        if (awaitingPromptSettle) {
          var preLine = readLogicalLineAroundCursor(term);
          if (preLine && preLine.active && !PASSWORD_PROMPT_RE.test(preLine.beforeCursor)) {
            var fallbackParsed = extractPromptFromLine(preLine.beforeCursor, null);
            if (fallbackParsed.matched) anchorPrefix = fallbackParsed.prompt;
          }
          awaitingPromptSettle = false;
        }
      }
      scheduleRefresh(false);
    }

    function onOutput(data) {
      if (!data || disposed) return;
      var wasEchoSuppressed = echoSuppressed;
      sentEchoBytesSinceOutput = 0;
      echoSuppressed = false;
      if (echoCheckTimer) { clearTimeout(echoCheckTimer); echoCheckTimer = null; }

      if (data.indexOf('\n') !== -1 || data.indexOf('\x1b[2J') !== -1 || data.indexOf('\x1b[?1049h') !== -1) {
        anchorPrefix = null;
        awaitingPromptSettle = true;
        pendingDeletes = 0;
        pendingAppend = '';
        lastScreenTyped = '';
        if (data.indexOf('\x1b[?1049h') !== -1) {
          hideUI();
        }
        return;
      }

      // Sync optimistic pending buffer against newly echoed terminal line
      var raw = readLogicalLineAroundCursor(term);
      if (raw && raw.active) {
        var parsed = extractPromptFromLine(raw.beforeCursor, anchorPrefix);
        if (parsed.matched) {
          var nowTyped = parsed.typed;
          if (pendingDeletes > 0) {
            var prevLen = Array.from(lastScreenTyped).length;
            var nowLen = Array.from(nowTyped).length;
            if (nowLen < prevLen) {
              pendingDeletes = Math.max(0, pendingDeletes - (prevLen - nowLen));
            } else if (pendingAppend && nowTyped.indexOf(pendingAppend) === 0) {
              pendingDeletes = 0;
              pendingAppend = '';
            } else if (nowTyped !== lastScreenTyped) {
              pendingDeletes = 0;
            }
          }
          if (pendingAppend) {
            if (nowTyped.indexOf(lastScreenTyped) === 0) {
              var delta = nowTyped.slice(lastScreenTyped.length);
              if (delta && pendingAppend.indexOf(delta) === 0) {
                pendingAppend = pendingAppend.slice(delta.length);
              } else if (delta) {
                pendingAppend = '';
              }
            } else if (pendingDeletes === 0) {
              if (nowTyped === pendingAppend || (nowTyped.length >= pendingAppend.length && nowTyped.slice(-pendingAppend.length) === pendingAppend)) {
                pendingAppend = '';
              } else {
                pendingAppend = '';
              }
            }
          }
          lastScreenTyped = nowTyped;
        }
      }

      var hasVisibleUI = !popupEl.hidden || (stripEl && !stripEl.hidden);
      var recentlyTyped = (Date.now() - lastEditAt) < 1500 && !suppressUntilEdit;
      if (hasVisibleUI || recentlyTyped || wasEchoSuppressed) {
        scheduleRefresh(false);
      }
    }

    function handleKeyDown(ev) {
      if (!enabled || disposed) return false;
      if (ev.type !== 'keydown') return false;

      var key = ev.key || '';
      var code = ev.code || '';

      // Ctrl+Space manually triggers autocomplete ONLY when at an active shell prompt
      if (ev.ctrlKey && !ev.altKey && !ev.metaKey && !ev.shiftKey && (code === 'Space' || key === ' ')) {
        var rawPrompt = readLogicalLineAroundCursor(term);
        if (!rawPrompt || !rawPrompt.active) return false;
        var parsedPrompt = extractPromptFromLine(rawPrompt.beforeCursor, anchorPrefix);
        if (!parsedPrompt.matched || parsedPrompt.isPassword) return false;

        dismissedQuery = null;
        suppressUntilEdit = false;
        selectedIdx = 0;
        userNavigated = false;
        renderActiveSuggestions(true);
        return true;
      }

      var hasSuggestions = currentItems.length > 0 && (!popupEl.hidden || (stripEl && !stripEl.hidden));
      if (!hasSuggestions) return false;

      if (key === 'Escape') {
        dismissedQuery = currentLineState ? currentLineState.effectiveLine : '';
        hideUI();
        return true;
      }
      if (key === 'Tab' && !ev.ctrlKey && !ev.altKey && !ev.metaKey && !ev.shiftKey) {
        acceptSuggestion(currentItems[selectedIdx] || currentItems[0]);
        return true;
      }
      if (key === 'ArrowRight' && !ev.ctrlKey && !ev.altKey && !ev.metaKey && !ev.shiftKey && !composingText) {
        if (currentLineState && !currentLineState.hasAfterCursor) {
          var target = currentItems[selectedIdx] || currentItems[0];
          if (target && target.cmd.indexOf(currentLineState.effectiveLine) === 0) {
            acceptSuggestion(target);
            return true;
          }
        }
      }

      // Desktop vertical navigation when floating popup is visible
      if (!popupEl.hidden) {
        if (key === 'ArrowDown') {
          selectedIdx = (selectedIdx + 1) % currentItems.length;
          userNavigated = true;
          renderActiveSuggestions(false);
          return true;
        }
        if (key === 'ArrowUp') {
          selectedIdx = (selectedIdx - 1 + currentItems.length) % currentItems.length;
          userNavigated = true;
          renderActiveSuggestions(false);
          return true;
        }
        if (key === 'Enter' && userNavigated && !ev.ctrlKey && !ev.altKey && !ev.metaKey && !ev.shiftKey) {
          acceptSuggestion(currentItems[selectedIdx] || currentItems[0]);
          return true;
        }
      }

      return false;
    }

    function setEnabled(next) {
      enabled = !!next;
      saveEnabledPref(enabled);
      if (!enabled) hideUI();
      else scheduleRefresh(false);
    }

    function refreshSnippets() {
      engine.setSnippets(loadSnippetsForAutocomplete(defaultSnippets));
    }

    loadServerHistory();

    return {
      isEnabled: function () { return enabled; },
      setEnabled: setEnabled,
      clearHistory: clearHistory,
      refreshSnippets: refreshSnippets,
      onUserInput: onUserInput,
      onCompositionChange: onCompositionChange,
      onOutput: onOutput,
      handleKeyDown: handleKeyDown,
      refreshLayout: function () {
        if (currentItems.length && (!popupEl.hidden || (stripEl && !stripEl.hidden))) {
          renderActiveSuggestions(false);
        }
      },
      hide: hideUI,
      dispose: function () {
        disposed = true;
        if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null; }
        if (echoCheckTimer) { clearTimeout(echoCheckTimer); echoCheckTimer = null; }
        if (ghostEl.parentNode) ghostEl.parentNode.removeChild(ghostEl);
        if (popupEl.parentNode) popupEl.parentNode.removeChild(popupEl);
        setStripShown(false);
      }
    };
  }

  window.NodexiaTermAutocomplete = {
    UBUNTU_COMMANDS: UBUNTU_COMMANDS,
    createEngine: createEngine,
    extractPromptFromLine: extractPromptFromLine,
    splitCommandChain: splitCommandChain,
    sanitizeCommandForStorage: sanitizeCommandForStorage,
    attach: attach
  };
})();
