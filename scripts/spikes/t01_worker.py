"""Bounded, one-turn AGY protocol probe; does not dispatch any Codex callback."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid


def is_open_design_pipe_active(endpoint: str) -> bool:
    pipe_name = endpoint.split('\\')[-1] if endpoint else ''
    try:
        pipes = os.listdir(r'\\.\pipe')
        if pipe_name:
            return pipe_name in pipes
        return any('open-design-sidecar' in p for p in pipes)
    except OSError:
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--thread', type=uuid.UUID, required=True)
    parser.add_argument('--input-mode', choices=['print', 'stream'], default='print')
    parser.add_argument('--isolate-mcp', action='store_true',
                        help='Disable known MCP names only in the temporary workspace')
    parser.add_argument('--auto-skip-open-design', action='store_true',
                        help='Temporarily disable open-design in global config if its sidecar pipe is inactive, restoring exact bytes in finally')
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    expected = 'MYO_T01_WORKER_OK'
    prompt = (
        'Teste isolado de protocolo. Responda exatamente MYO_T01_WORKER_OK. '
        'Nao use ferramentas, leia arquivos ou execute comandos. Encerre o turno.'
    )
    command = [
        os.path.expandvars(r'%USERPROFILE%\AppData\Local\agy\bin\agy.EXE'),
        '--output-format', 'stream-json', '--mode', 'plan',
        '--sandbox', '--print-timeout', '60s',
    ]
    if args.input_mode == 'stream':
        command += ['--input-format', 'stream-json']
        stdin_data = (json.dumps({'event': 'user', 'message': {'content': prompt}}) + '\n').encode('utf-8')
    else:
        command += ['--print', prompt]
        stdin_data = None
    report = {
        'probe_id': str(uuid.uuid4()),
        'started_at': datetime.now(timezone.utc).isoformat(),
        'coordinator_thread_id': str(args.thread),
        'worker_turn_limit': 1,
        'input_mode': args.input_mode,
        'workspace_mcp_override': args.isolate_mcp,
        'auto_skip_open_design': args.auto_skip_open_design,
        'worker_timeout_seconds': 60,
        'requested_model': 'existing CLI default; no override',
        'expected_response': expected,
        'callback_attempted': False,
        'callback_status': 'not attempted; host return uses synchronous bridge process stdout',
    }
    global_config_path = Path.home() / '.gemini/config/mcp_config.json'
    original_global_bytes = None
    global_modified = False
    if args.auto_skip_open_design and global_config_path.exists():
        original_global_bytes = global_config_path.read_bytes()
        report['global_mcp_sha256_before'] = hashlib.sha256(original_global_bytes).hexdigest()
        cfg = json.loads(original_global_bytes)
        od = cfg.get('mcpServers', {}).get('open-design', {})
        endpoint = od.get('env', {}).get('OD_SIDECAR_CLIENT_ENDPOINT', '')
        pipe_active = is_open_design_pipe_active(endpoint)
        report['open_design_pipe_active'] = pipe_active
        report['skipped_mcp_servers'] = sorted(cfg.get('mcpServers', {}).keys())
        cfg['mcpServers'] = {}
        global_config_path.write_text(json.dumps(cfg, indent=2) + '\n', encoding='utf-8')
        global_modified = True
        report['open_design_temporarily_disabled'] = True
    try:
        # Only the isolated temporary worker directory is automatically removed.
        with tempfile.TemporaryDirectory(prefix='myo-t01-worker-', ignore_cleanup_errors=True) as cwd:
            if args.isolate_mcp:
                config_dir = Path(cwd) / '.agents'
                config_dir.mkdir()
                names = ['browsermcp', 'context7', 'open-design', 'playwright', 'shadcn']
                (config_dir / 'mcp_config.json').write_text(json.dumps({
                    'mcpServers': {name: {'disabled': True} for name in names}
                }), encoding='utf-8')
                report['workspace_disabled_mcp_names'] = names
                report['workspace_override_effective'] = 'unknown; inspect CLI log'
            try:
                result = subprocess.run(command, cwd=cwd, input=stdin_data,
                                        capture_output=True, timeout=75)
                stdout, stderr = result.stdout, result.stderr
                report['exit_code'] = result.returncode
            except subprocess.TimeoutExpired as error:
                stdout, stderr = error.stdout or b'', error.stderr or b''
                report['exit_code'] = None
                report['failure'] = 'outer process timeout; no automatic retry'
    finally:
        if original_global_bytes is not None:
            if global_modified:
                global_config_path.write_bytes(original_global_bytes)
            after_bytes = global_config_path.read_bytes()
            report['global_mcp_sha256_after'] = hashlib.sha256(after_bytes).hexdigest()
            report['global_mcp_restored_verified'] = (
                report['global_mcp_sha256_before'] == report['global_mcp_sha256_after']
            )
    (args.output / 'worker.stdout.jsonl').write_bytes(stdout)
    (args.output / 'worker.stderr.log').write_bytes(stderr)
    events, invalid = [], 0
    for line in stdout.decode('utf-8', errors='replace').splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            invalid += 1
    report['event_types'] = [e.get('event', e.get('type')) for e in events if isinstance(e, dict)]
    report['invalid_json_lines'] = invalid
    finals = [e['result'] for e in events if isinstance(e, dict)
              and e.get('event') == 'result' and isinstance(e.get('result'), dict)]
    report['terminal_result_count'] = len(finals)
    report['terminal_results'] = finals
    report['timeout_reported'] = 'print timeout' in stderr.decode('utf-8', errors='replace').lower()
    report['worker_succeeded'] = (
        report['exit_code'] == 0 and len(finals) == 1
        and not report['timeout_reported']
        and finals[0].get('status') == 'SUCCESS'
        and finals[0].get('response', '').strip() == expected
    )
    report['finished_at'] = datetime.now(timezone.utc).isoformat()
    (args.output / 'result.json').write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=True))
    return 0 if report['worker_succeeded'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
