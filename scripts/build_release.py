#!/usr/bin/env python3
"""Build whitelisted release archives; smoke native binaries without user data."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def run(*args, env=None):
    return subprocess.check_output(args, cwd=ROOT, env=env, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', action='append', required=True,
                        choices=['darwin/amd64', 'darwin/arm64', 'windows/amd64'])
    args = parser.parse_args()
    if run('git', 'status', '--porcelain'):
        raise SystemExit('Release builds require a clean worktree')
    version = run('go', 'run', './cmd/wxview', 'version')
    if not re.fullmatch(r'\d+\.\d+\.\d+', version):
        raise SystemExit('Unexpected release version: ' + version)
    if os.environ.get('GITHUB_REF_TYPE') == 'tag' and os.environ['GITHUB_REF_NAME'] != 'v' + version:
        raise SystemExit('Tag and source version differ')
    commit = run('git', 'rev-parse', 'HEAD')
    epoch = int(run('git', 'show', '-s', '--format=%ct', 'HEAD'))
    host_os = {'Darwin': 'darwin', 'Windows': 'windows', 'Linux': 'linux'}[platform.system()]
    host_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'amd64': 'amd64', 'x86_64': 'amd64'}[platform.machine().lower()]
    dist = ROOT / 'dist'
    dist.mkdir(exist_ok=True)
    manifest = {'version': version, 'commit': commit, 'packages': []}
    docs = ['README.md', 'docs/lldb-setup.md', 'docs/windows-key-scan.md',
            'docs/wal-snapshots.md', 'docs/query-coverage.md', f'docs/releases/v{version}.md',
            'docs/third-party/wcdb-key-tool.LICENSE', 'docs/third-party/wechat-decrypt.LICENSE',
            'internal/key/lldbhelper/LICENSE', 'internal/key/lldbhelper/NOTICE']
    for target in dict.fromkeys(args.target):
        goos, goarch = target.split('/')
        env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED='0')
        name = f'wxview_{version}_{goos}_{goarch}'
        with tempfile.TemporaryDirectory(prefix='wxview-release-') as temp:
            stage = Path(temp) / name
            stage.mkdir()
            binary = stage / ('wxview.exe' if goos == 'windows' else 'wxview')
            run('go', 'build', '-trimpath', '-mod=readonly', '-buildvcs=true',
                '-ldflags=-s -w', '-o', str(binary), './cmd/wxview', env=env)
            build_info = run('go', 'version', '-m', str(binary))
            for field in [f'vcs.revision={commit}', 'vcs.modified=false', f'GOOS={goos}', f'GOARCH={goarch}']:
                if field not in build_info:
                    raise SystemExit('Missing binary provenance: ' + field)
            for rel in docs:
                dest = stage / rel
                dest.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / rel, dest)
            # Include notices from modules actually linked for this target.
            raw = run('go', 'list', '-deps', '-json', './cmd/wxview', env=env)
            decoder, modules = json.JSONDecoder(), {}
            while raw:
                item, end = decoder.raw_decode(raw)
                module = item.get('Module', {})
                if module and not module.get('Main'):
                    modules[module['Path']] = module
                raw = raw[end:].lstrip()
            for module in modules.values():
                for source in Path(module['Dir']).iterdir():
                    if source.is_file() and source.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')):
                        dest = stage / 'licenses' / 'go-modules' / (module['Path'] + '@' + module['Version']) / source.name
                        dest.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copyfile(source, dest)
            dest = stage / 'licenses' / 'GO-LICENSE'
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(Path(run('go', 'env', 'GOROOT')) / 'LICENSE', dest)
            native = (goos, goarch) == (host_os, host_arch)
            if native:
                home = Path(temp) / 'smoke-home'
                home.mkdir()
                smoke_env = dict(os.environ, HOME=str(home), USERPROFILE=str(home), APPDATA=str(home))
                for command in [('version',), ('--version',)]:
                    if run(str(binary), *command, env=smoke_env) != version:
                        raise SystemExit('Version smoke failed')
                for command in [('--help',), ('init', '--help'), ('contacts',), ('messages',)]:
                    output = run(str(binary), *command, env=smoke_env)
                    if 'wxview' not in output.lower():
                        raise SystemExit('Help smoke failed')
                if (home / '.wxview').exists():
                    raise SystemExit('Smoke unexpectedly initialized runtime state')
            metadata = {'version': version, 'commit': commit, 'target': target,
                        'native_smoke': native, 'go_version': run('go', 'version'),
                        'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
            (stage / 'BUILD-INFO.json').write_text(json.dumps(metadata, indent=2) + '\n', encoding='utf-8')
            archive = dist / (name + ('.zip' if goos == 'windows' else '.tar.gz'))
            if goos == 'windows':
                with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as zf:
                    for path in sorted(stage.rglob('*')):
                        if path.is_file():
                            zf.write(path, str(path.relative_to(stage.parent)))
            else:
                def clean(info):
                    info.uid = info.gid = 0
                    info.uname = info.gname = ''
                    info.mtime = epoch
                    info.mode = 0o755 if info.isdir() or info.name.endswith('/wxview') else 0o644
                    return info
                with tarfile.open(archive, 'w:gz') as tf:
                    tf.add(stage, arcname=name, filter=clean)
            metadata.update(file=archive.name, size=archive.stat().st_size,
                            sha256=hashlib.sha256(archive.read_bytes()).hexdigest())
            manifest['packages'].append(metadata)
            print(f'{archive.name}: native_smoke={native}', flush=True)
    (dist / f'build-manifest-{host_os}.json').write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')


if __name__ == '__main__':
    main()
