"""Synthetic tests only: no WeChat app, account, database or real key is used.

Run pure tests: python3 -B -m unittest discover -s internal/key/lldbhelper
Run LLDB integration on macOS ARM64 or Intel with CLT:
  WXVIEW_TEST_LLDB=1 /usr/bin/python3 -B -m unittest discover -s internal/key/lldbhelper
"""
import hashlib
import hmac
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import tempfile
import unittest

import capture


def page(key, salt):
    body = bytes(4016)
    mk = hashlib.pbkdf2_hmac('sha512', key, bytes(x ^ 0x3a for x in salt), 2, 32)
    return salt + body + hmac.new(mk, body + b'\x01\x00\x00\x00', hashlib.sha512).digest()


class CryptoTests(unittest.TestCase):
    def test_both_pbkdf_paths_and_wrong_key(self):
        salt = b'\x11' * 16
        secret = b'synthetic-password'
        raw = hashlib.pbkdf2_hmac('sha512', secret, salt, 256000, 32)
        macsalt = bytes(x ^ 0x3a for x in salt)
        salts, macsalts = {salt}, {macsalt: salt}
        self.assertEqual(capture.candidate(secret, salt, 256000, salts, macsalts), (salt, raw))
        self.assertEqual(capture.candidate(raw, macsalt, 2, salts, macsalts), (salt, raw))
        self.assertTrue(capture.verified(raw, page(raw, salt)))
        self.assertFalse(capture.verified(bytes(32), page(raw, salt)))
        self.assertEqual(capture.candidate(raw, bytes(16), 2, salts, macsalts), (None, None))

    def test_private_atomic_candidate_file(self):
        with tempfile.TemporaryDirectory() as work:
            out = Path(work) / 'candidates.json'
            capture.save(out, {'a': {'salt': 'test', 'enc_key': 'synthetic'}})
            capture.save(out, {})
            self.assertEqual(json.loads(out.read_text()), {'version': 1, 'keys': {}})
            self.assertEqual(out.stat().st_mode & 0o777, 0o600)
            self.assertFalse(out.with_suffix('.new').exists())

    def test_permission_diagnostic_does_not_return_log(self):
        with tempfile.TemporaryDirectory() as work:
            log = Path(work) / 'app-stderr.log'
            log.write_text('private user content: Operation not permitted')
            self.assertIs(capture.permission_denied(log), True)


@unittest.skipUnless(os.environ.get('WXVIEW_TEST_LLDB') == '1' and
                     platform.system() == 'Darwin' and platform.machine() in ('arm64', 'x86_64'),
                     'opt-in Apple LLDB integration')
class LLDBTests(unittest.TestCase):
    def run_fixture(self, mode):
        with tempfile.TemporaryDirectory(prefix='wxview-lldb-test-') as work:
            root = Path(work).resolve()
            source = root / 'probe.c'
            source.write_text(r'''
#include <CommonCrypto/CommonKeyDerivation.h>
#include <string.h>
#include <unistd.h>
int main(void) {
 unsigned char salt[16], raw[32], output[32];
 memset(salt, 0x11, 16);
 const char *secret = "synthetic-password";
 CCKeyDerivationPBKDF(kCCPBKDF2, secret, strlen(secret), salt, 16, kCCPRFHmacAlgSHA512, 256000, raw, 32);
 memset(raw, 0x22, 32); memset(salt, 0x33 ^ 0x3a, 16);
 CCKeyDerivationPBKDF(kCCPBKDF2, (const char *)raw, 32, salt, 16, kCCPRFHmacAlgSHA512, 2, output, 32);
 sleep(30);
 return 0;
}
''')
            exe = root / 'probe'
            subprocess.run(['/usr/bin/xcrun', 'clang', '-O0', str(source), '-o', str(exe)], check=True, capture_output=True)
            subprocess.run(['/usr/bin/codesign', '--force', '--sign', '-', str(exe)], check=True, capture_output=True)
            raw = hashlib.pbkdf2_hmac('sha512', b'synthetic-password', b'\x11'*16, 256000, 32)
            expected = {'first.db': (raw, b'\x11'*16), 'second.db': (b'\x22'*32, b'\x33'*16)}
            if mode != 'complete':
                expected['unopened.db'] = (b'\x44'*32, b'\x55'*16)
            for rel, (key, salt) in expected.items():
                (root / rel).write_bytes(page(key, salt))
            targets = root / 'targets.json'
            targets.write_text(json.dumps({'root': str(root), 'targets': list(expected)}))
            out = root / 'candidates.json'
            env = dict(os.environ, PYTHONPATH=subprocess.check_output(['/usr/bin/lldb', '-P'], text=True).strip(),
                       PYTHONDONTWRITEBYTECODE='1', PYTHONNOUSERSITE='1')
            p = subprocess.Popen(['/usr/bin/python3', '-s', str(Path(capture.__file__).resolve()),
                                  '--exe', str(exe), '--targets', str(targets), '--out', str(out), '--timeout', '4'],
                                 env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            initial = []
            if mode == 'cancel':
                while True:
                    line = p.stdout.readline()
                    if not line:
                        break
                    event = json.loads(line)
                    initial.append(event)
                    if event.get('type') == 'launched':
                        p.send_signal(signal.SIGINT)
                        break
            stdout, stderr = p.communicate(timeout=25)
            events = initial + [json.loads(line) for line in stdout.splitlines()]
            self.assertEqual(stderr, '')
            last = events[-1]
            self.assertTrue(last['closed'], events)
            self.assertEqual(last['reason'], {'cancel': 'cancelled'}.get(mode, mode), events)
            self.assertEqual(p.returncode, 2 if mode == 'cancel' else 0)
            for e in events:
                if e['type'] == 'launched':
                    with self.assertRaises(ProcessLookupError):
                        os.kill(e['pid'], 0)
            if mode != 'cancel':
                keys = json.loads(out.read_text())['keys']
                self.assertEqual(set(keys), {'first.db', 'second.db'})
                for rel in keys:
                    self.assertEqual(keys[rel]['enc_key'], expected[rel][0].hex())
                self.assertEqual(out.stat().st_mode & 0o777, 0o600)

    def test_complete(self):
        self.run_fixture('complete')

    def test_timeout(self):
        self.run_fixture('timeout')

    def test_cancel(self):
        self.run_fixture('cancel')


if __name__ == '__main__':
    unittest.main()
