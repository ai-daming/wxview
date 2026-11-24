"""Generate ONLY invented contact/message/session data with real SQLite WAL.
Go tests use the embedded result; Python/native SQLite is needed only to regenerate.
"""
import base64, ctypes as c, ctypes.util, gzip, hashlib, json, sqlite3, tempfile
from pathlib import Path
with tempfile.TemporaryDirectory() as directory:
    path=Path(directory)/'synthetic.db'
    api=c.CDLL(ctypes.util.find_library('sqlite3'))
    api.sqlite3_open.argtypes=[c.c_char_p,c.POINTER(c.c_void_p)]
    api.sqlite3_file_control.argtypes=[c.c_void_p,c.c_char_p,c.c_int,c.c_void_p]
    api.sqlite3_exec.argtypes=[c.c_void_p,c.c_char_p,c.c_void_p,c.c_void_p,c.c_void_p]
    api.sqlite3_close.argtypes=[c.c_void_p]
    handle=c.c_void_p(); assert api.sqlite3_open(str(path).encode(),c.byref(handle))==0
    table='Msg_'+hashlib.md5(b'alice').hexdigest()
    sql=f'''PRAGMA page_size=4096;
CREATE TABLE contact(id INTEGER,username TEXT,local_type INTEGER,alias TEXT,remark TEXT,nick_name TEXT,big_head_url TEXT);
INSERT INTO contact VALUES(1,'alice',3,'','Alice','Alice',''),(2,'synthetic_self',3,'','Self','Self','');
CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY,is_session INTEGER);
INSERT INTO Name2Id(rowid,user_name,is_session) VALUES(1,'alice',1),(2,'synthetic_self',0);
CREATE TABLE {table}(local_id INTEGER PRIMARY KEY,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,status INTEGER,message_content BLOB,WCDB_CT_message_content INTEGER);
INSERT INTO {table} VALUES(1,1,1,1000,1,100,4,'baseline',0);
CREATE TABLE SessionTable(username TEXT PRIMARY KEY,unread_count INTEGER,summary BLOB,last_timestamp INTEGER,last_msg_type INTEGER,last_msg_sender TEXT,last_sender_display_name TEXT);
INSERT INTO SessionTable VALUES('alice',1,'baseline',100,1,'alice','Alice');'''
    try:
        reserve=c.c_int(80);assert api.sqlite3_file_control(handle,b'main',38,c.byref(reserve))==0
        assert api.sqlite3_exec(handle,sql.encode(),None,None,None)==0
    finally: assert api.sqlite3_close(handle)==0
    conn=sqlite3.connect(path);assert conn.execute('PRAGMA journal_mode=WAL').fetchone()==('wal',)
    conn.execute('PRAGMA wal_autocheckpoint=0');conn.execute('PRAGMA wal_checkpoint(TRUNCATE)')
    main=path.read_bytes();assert main[20]==80
    output={'main':base64.b64encode(main).decode()}
    for i,text in [(2,'wal-first'),(3,'wal-second')]:
        conn.execute(f'INSERT INTO {table} VALUES(?,?,1,?,1,?,4,?,0)',(i,i,i*1000,i*100,text))
        conn.execute('UPDATE SessionTable SET unread_count=?,summary=?,last_timestamp=?',(i,text,i*100));conn.commit()
        output['wal'+str(i-1)]=base64.b64encode(Path(str(path)+'-wal').read_bytes()).decode()
        assert path.read_bytes()==main, 'Fixture unexpectedly checkpointed'
    assert conn.execute('PRAGMA integrity_check').fetchone()==('ok',)
    conn.close()
    packed=gzip.compress(json.dumps(output).encode(),mtime=0)
    Path(__file__).with_name('fixture.b64').write_text(base64.b64encode(packed).decode()+'\n')
    print('Generated synthetic SQLite/WAL fixture')
