#!/usr/bin/env python3
"""Capture only the selected account's keys from our own LLDB child.

Adapted from DaneelOlivaw1/macos-wechat-export capture_keys.py at
f6c5fb32306945fcb95740c3c2e70cbe548317dc. See embedded LICENSE and NOTICE.
Changes: bounded target list, structured completion/cleanup, safe diagnostics,
versioned candidates, and cancellation handling. Never attach to another app.
"""
import argparse
import hashlib
import hmac
import json
import os
from pathlib import Path
import signal
import time


def emit(kind, **fields):
    print(json.dumps(dict(type=kind, **fields)), flush=True)


def verified(key, page):
    if len(key) != 32 or len(page) != 4096:
        return False
    mac_key = hashlib.pbkdf2_hmac('sha512', key, bytes(x ^ 0x3a for x in page[:16]), 2, 32)
    actual = hmac.new(mac_key, page[16:4032] + b'\x01\x00\x00\x00', hashlib.sha512).digest()
    return hmac.compare_digest(actual, page[4032:4096])


def candidate(secret, salt_arg, rounds, salts, macsalts):
    salt = salt_arg if rounds == 256000 else macsalts.get(salt_arg)
    if salt not in salts:
        return None, None
    key = hashlib.pbkdf2_hmac('sha512', secret, salt, 256000, 32) if rounds == 256000 else secret
    return (salt, key) if len(key) == 32 else (None, None)


def save(path, found):
    tmp = path.with_suffix('.new')
    fd = os.open(str(tmp), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        with os.fdopen(fd, 'w') as f:
            json.dump({'version': 1, 'keys': found}, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(str(tmp), str(path))
    finally:
        if tmp.exists():
            tmp.unlink()


def permission_denied(path):
    try:
        with path.open('rb') as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - 65536))
            tail = f.read().lower()
        return b'operation not permitted' in tail or b'permission denied' in tail
    except OSError:
        return False


def run(exe, target_file, out, timeout):
    import lldb
    spec = json.loads(target_file.read_text())
    root = Path(spec['root']).resolve(strict=True)
    rows = []
    for rel in spec['targets']:
        path = root / rel
        if path.resolve(strict=True) != path or root not in path.parents:
            raise RuntimeError('invalid target')
        with path.open('rb') as f:
            page = f.read(4096)
        if len(page) == 4096 and not page.startswith(b'SQLite format 3\0'):
            rows.append((rel, page))
    if not rows:
        raise RuntimeError('no encrypted targets')
    salts = {page[:16] for _, page in rows}
    macsalts = {bytes(x ^ 0x3a for x in salt): salt for salt in salts}
    debugger = lldb.SBDebugger.Create()
    debugger.SkipLLDBInitFiles(True)
    debugger.SetAsync(True)
    process = None
    found = {}
    reason = 'capture_failed'
    closed = True
    app_log = out.parent / 'app-stderr.log'
    try:
        target = debugger.CreateTarget(str(exe))
        if not target.IsValid():
            raise RuntimeError('unsupported target')
        arch = (target.GetTriple() or '').split('-')[0]
        if arch not in ('arm64', 'arm64e', 'x86_64'):
            raise RuntimeError('unsupported target')
        # Stack arguments on x86_64 must be read before the function prologue.
        if arch == 'x86_64':
            debugger.HandleCommand('settings set target.skip-prologue false')
        bp = target.BreakpointCreateByName('CCKeyDerivationPBKDF')
        if not bp.IsValid():
            raise RuntimeError('breakpoint unavailable')
        launch = lldb.SBLaunchInfo([])
        launch.SetWorkingDirectory(str(exe.parent))
        launch.AddSuppressFileAction(0, True, False)
        launch.AddSuppressFileAction(1, False, True)
        launch.AddOpenFileAction(2, str(app_log), False, True)
        err = lldb.SBError()
        reason = 'launch_failed'
        process = target.Launch(launch, err)
        if err.Fail() or not process.IsValid():
            raise RuntimeError('launch failed')
        closed = False
        reason = 'capture_failed'
        emit('launched', pid=process.GetProcessID(), total=len(rows))
        listener = debugger.GetListener()
        deadline = time.monotonic() + timeout
        update = time.monotonic()
        def mem(addr, size):
            e = lldb.SBError()
            data = process.ReadMemory(addr, size, e)
            return bytes(data) if e.Success() and len(data) == size else b''
        while time.monotonic() < deadline:
            event = lldb.SBEvent()
            if listener.WaitForEvent(1, event) and lldb.SBProcess.EventIsProcessEvent(event):
                state = lldb.SBProcess.GetStateFromEvent(event)
                if state in (lldb.eStateExited, lldb.eStateCrashed, lldb.eStateDetached):
                    reason = 'permission_denied' if permission_denied(app_log) else 'process_ended'
                    break
                if state == lldb.eStateStopped:
                    hit = False
                    for thread in process:
                        if thread.GetStopReason() != lldb.eStopReasonBreakpoint:
                            continue
                        if thread.GetStopReasonDataCount() < 2 or thread.GetStopReasonDataAtIndex(0) != bp.GetID():
                            continue
                        hit = True
                        frame = thread.GetFrameAtIndex(0)
                        if arch == 'x86_64':
                            args = [frame.FindRegister(r).GetValueAsUnsigned()
                                    for r in ('rsi', 'rdx', 'rcx', 'r8', 'r9')]
                            stack = frame.FindRegister('rsp').GetValueAsUnsigned()
                            rounds_bytes = mem(stack + 8, 4)
                            if len(rounds_bytes) != 4:
                                continue
                            args.append(int.from_bytes(rounds_bytes, 'little'))
                        else:
                            args = [frame.FindRegister('x' + str(i)).GetValueAsUnsigned() for i in range(1, 7)]
                        pp, pl, sp, sl, prf, rounds = args
                        if not (sl == 16 and 0 < pl <= 256 and prf == 5 and rounds in (2, 256000)):
                            continue
                        salt_arg = mem(sp, 16)
                        if salt_arg not in (salts if rounds == 256000 else macsalts):
                            continue
                        secret = mem(pp, pl)
                        if len(secret) != pl:
                            continue
                        salt, key = candidate(secret, salt_arg, rounds, salts, macsalts)
                        changed = False
                        for rel, page in rows:
                            if rel not in found and page[:16] == salt and key is not None and verified(key, page):
                                found[rel] = {'salt': salt.hex(), 'enc_key': key.hex()}
                                changed = True
                        if changed:
                            save(out, found)
                            emit('progress', captured=len(found), total=len(rows))
                    if not hit:
                        reason = 'unexpected_stop'
                        break
                    e = process.Continue()
                    if e.Fail():
                        raise RuntimeError('resume failed')
            if len(found) == len(rows):
                reason = 'complete'
                break
            if time.monotonic() - update >= 15:
                emit('waiting', captured=len(found), total=len(rows))
                update = time.monotonic()
        else:
            reason = 'timeout'
    except KeyboardInterrupt:
        reason = 'cancelled'
    except Exception:
        # Never interpolate exceptions or target application output: either may
        # contain process memory, private paths, or sensitive database material.
        if permission_denied(app_log):
            reason = 'permission_denied'
    finally:
        if process is not None and process.IsValid():
            if process.GetState() == lldb.eStateExited:
                closed = True
            else:
                try:
                    closed = process.Kill().Success()
                except Exception:
                    closed = False
        lldb.SBDebugger.Destroy(debugger)
    emit('finished', reason=reason, closed=closed, captured=len(found), total=len(rows))
    return 0 if closed and reason in ('complete', 'timeout') else 2


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--exe', required=True)
    ap.add_argument('--targets', required=True)
    ap.add_argument('--out', required=True)
    ap.add_argument('--timeout', type=int, default=300)
    args = ap.parse_args()
    os.umask(0o077)
    signal.signal(signal.SIGTERM, signal.default_int_handler)
    out = Path(args.out)
    if out.exists() or not 1 <= args.timeout <= 300:
        raise RuntimeError('invalid output or timeout')
    return run(Path(args.exe).resolve(strict=True), Path(args.targets), out, args.timeout)


if __name__ == '__main__':
    try:
        code = main()
    except BaseException:
        emit('finished', reason='setup_failed', closed=False, captured=0, total=0)
        code = 2
    raise SystemExit(code)
