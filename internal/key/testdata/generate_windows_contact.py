"""Regenerate the synthetic contact fixture; never reads WeChat/user data.

Uses SQLite's public SQLITE_FCNTL_RESERVE_BYTES API (38) to create the real
80-byte reserved area, rather than modifying SQLite page headers by hand.
Only fixture regeneration uses Python/native SQLite; Go tests do not.
"""
import base64
import ctypes as c
import ctypes.util
import gzip
from pathlib import Path
import sqlite3
import tempfile

with tempfile.TemporaryDirectory() as directory:
    path = Path(directory) / "synthetic.db"
    api = c.CDLL(ctypes.util.find_library("sqlite3"))
    api.sqlite3_open.argtypes = [c.c_char_p, c.POINTER(c.c_void_p)]
    api.sqlite3_file_control.argtypes = [c.c_void_p, c.c_char_p, c.c_int, c.c_void_p]
    api.sqlite3_close.argtypes = [c.c_void_p]
    connection = c.c_void_p()
    assert api.sqlite3_open(str(path).encode(), c.byref(connection)) == 0
    # Persist reserve size and schema through the same connection.
    api.sqlite3_exec.argtypes = [c.c_void_p, c.c_char_p, c.c_void_p, c.c_void_p, c.c_void_p]
    try:
        reserved = c.c_int(80)
        assert api.sqlite3_file_control(connection, b"main", 38, c.byref(reserved)) == 0
        sql = b"""
        PRAGMA page_size=4096;
        CREATE TABLE contact(id INTEGER, username TEXT, local_type INTEGER,
          alias TEXT, remark TEXT, nick_name TEXT, big_head_url TEXT);
        INSERT INTO contact VALUES(1,'synthetic_friend',1,'fixture_alias',
          'Fixture contact','Fixture nickname','');
        """
        assert api.sqlite3_exec(connection, sql, None, None, None) == 0
    finally:
        assert api.sqlite3_close(connection) == 0
    with sqlite3.connect(path) as check:
        assert check.execute("PRAGMA integrity_check").fetchone() == ("ok",)
        assert check.execute("SELECT count(*) FROM contact").fetchone() == (1,)
    data = path.read_bytes()
    assert data[20] == 80 and len(data) % 4096 == 0
    output = Path(__file__).with_name("windows_contact.b64")
    output.write_text(base64.b64encode(gzip.compress(data, mtime=0)).decode() + "\n")
    print(f"Generated {output.name}: {len(data)} synthetic SQLite bytes")
