"""Read-only AGY config audit; never starts a prompt or a configured MCP command."""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess


def main():
    root = Path(__file__).resolve().parents[2]
    output = root / 'work/spikes/mcp-config'
    workspace = output / 'workspace'
    local_config = workspace / '.agents/mcp_config.json'
    local_config.parent.mkdir(parents=True, exist_ok=True)
    cli = Path.home() / 'AppData/Local/agy/bin/agy.EXE'
    global_config = Path.home() / '.gemini/config/mcp_config.json'
    original = global_config.read_bytes()
    config = json.loads(original)
    names = sorted(config['mcpServers'])
    sentinel = 'myo-local-config-sentinel'
    local_config.write_text(json.dumps({'mcpServers': {
        **{name: {'disabled': True} for name in names},
        sentinel: {'command': 'myo-never-execute-sentinel', 'disabled': True}
    }}, indent=2) + '\n', encoding='utf-8')
    report = {
        'checked_at': datetime.now(timezone.utc).isoformat(),
        'inference_commands': 0,
        'scope': 'mcp list inspects user-level configuration; not a backend startup test',
        'global_config_path': str(global_config),
        'global_config_sha256_before': hashlib.sha256(original).hexdigest(),
        'global_servers': [{'name': name,
                            'disabled': config['mcpServers'][name].get('disabled', False)}
                           for name in names],
        'local_config_path': str(local_config),
        'cli_sha256': hashlib.sha256(cli.read_bytes()).hexdigest(),
        'observations': []
    }
    for label, cwd in [('project', root), ('local_override', workspace)]:
        result = subprocess.run([str(cli), 'mcp', 'list'], cwd=cwd,
                                capture_output=True, timeout=10)
        rows = []
        for line in result.stdout.decode('utf-8', errors='replace').splitlines()[1:]:
            cells = line.split()
            if len(cells) >= 3:
                rows.append({'name': cells[0], 'type': cells[1], 'status': cells[2]})
        report['observations'].append({'cwd_kind': label, 'exit_code': result.returncode,
                                       'servers': rows, 'stderr_present': bool(result.stderr)})
    binary = cli.read_bytes()
    start = binary.find(b'MCP servers are defined in a')
    end = binary.find(b'## Configuration Schema', start)
    if start >= 0 and start < end < start + 4000:
        (output / 'embedded-mcp-location.txt').write_text(
            binary[start:end].decode('utf-8', errors='replace'), encoding='utf-8')
    report['literal_workspace_mcp_path_in_binary'] = b'.agents/mcp_config.json' in binary
    report['global_config_sha256_after'] = hashlib.sha256(global_config.read_bytes()).hexdigest()
    report['global_config_unchanged'] = (
        report['global_config_sha256_before'] == report['global_config_sha256_after'])
    (output / 'audit.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=True))
    return 0 if report['global_config_unchanged'] and all(
        item['exit_code'] == 0 for item in report['observations']) else 1


if __name__ == '__main__':
    raise SystemExit(main())
