#!/usr/bin/env python3
"""Root-only disposable nas.example probe. Password JSON is read from stdin, never logged.
Requires pre-created p2probe{1,2,adm,pkg}, protected runtime, home-scoped policy.
No tasks, installed packages or global DSM settings are changed.
"""
import json, os, pathlib, pwd, socket, subprocess, sys, urllib.parse, urllib.request

creds = json.load(sys.stdin)
BASE = pathlib.Path('/usr/local/etc/synopkgland/p2probepkg')
SOCK = '/run/synopkgland/p2probepkg/executor.sock'
STAGE = pathlib.Path('/var/packages/p2probepkg/var/executor-staging')
sessions = {}

def api(params):
    data = urllib.parse.urlencode(params).encode()
    with urllib.request.urlopen('http://127.0.0.1:5000/webapi/entry.cgi', data, timeout=15) as r:
        return json.load(r)

def login(name):
    r = api({'api':'SYNO.API.Auth','method':'login','version':'7','account':name,'passwd':creds[name],'format':'sid','enable_syno_token':'yes'})
    assert r.get('success'), ('login failed', name, r.get('error'))
    sessions[name] = r['data']
    return sessions[name]['sid']

def run_api(api_name, method, **params):
    args = ['/usr/syno/bin/synowebapi','--exec','api='+api_name,'method='+method,'version=1']
    args += [k+'='+json.dumps(v) for k,v in params.items()]
    r = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    return json.loads(r.stdout)

def call(op, params=None, cookie=None, peer='p2probepkg'):
    req = {'v':1,'op':op,'params':params or {}}
    if cookie: req['auth'] = {'dsm_cookie':cookie,'dsm_synotoken':next((v['synotoken'] for v in sessions.values() if v['sid']==cookie),'')}
    # Launch a disposable package-uid client, with no root client shortcut.
    u = pwd.getpwnam(peer)
    def identity():
        os.setgroups(os.getgrouplist(peer,u.pw_gid));os.setgid(u.pw_gid);os.setuid(u.pw_uid)
    script = 'import socket,sys;s=socket.socket(socket.AF_UNIX);s.settimeout(35);s.connect(sys.argv[1]);s.sendall(sys.stdin.buffer.read());print(s.recv(1048576).decode())'
    r = subprocess.run(['/usr/bin/python3','-c',script,SOCK],input=(json.dumps(req)+'\n').encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=identity)
    raw = r.stdout.decode().strip()
    return json.loads(raw) if raw else {'closed':True}

def check(label, response, code=None):
    print(json.dumps({'scenario':label,'response':response}),flush=True)
    assert response.get('ok') if code is None else response.get('code')==code, label

def create(cookie, path='/home'):
    r=call('delegate.create',{'job_id':'p2probejob','ops':['files.write','files.list','files.info','files.createFolder','files.checkPermission'],'scope':'home','path':path,'ttl_seconds':600},cookie)
    check('delegate.create '+path,r)
    return r['result']['delegation_id']

try:
    alice=login('p2probe1');bob=login('p2probe2');admin=login('p2probeadm')
    check('status',call('status'))
    denied=call('status',peer='p2probe2');print(json.dumps({'scenario':'wrong peer','response':denied}));assert denied.get('closed')
    delegation=create(alice)
    p={'delegation_id':delegation,'job_id':'p2probejob','staging':'p2probe-source','dest_dir':'/home','name':'p2probe-output'}
    STAGE.joinpath('p2probe-source').write_text('P2 executor content\n')
    os.chown(str(STAGE/'p2probe-source'),pwd.getpwnam('p2probepkg').pw_uid,pwd.getpwnam('p2probepkg').pw_gid)
    check('files.write own home',call('files.write',p))
    out=pathlib.Path('/var/services/homes/p2probe1/p2probe-output');st=out.stat();assert st.st_uid==pwd.getpwnam('p2probe1').pw_uid
    print(json.dumps({'scenario':'owner','uid':st.st_uid,'gid':st.st_gid,'mode':oct(st.st_mode&0o777),'content':out.read_text()}))
    acl=subprocess.run(['/usr/syno/bin/synoacltool','-get',str(out)],stdout=subprocess.PIPE,stderr=subprocess.STDOUT).stdout.decode()
    print(json.dumps({'scenario':'ACL readback','acl':acl}))
    assert 'inherited' in acl.lower() or 'level:1' in acl.lower() or 'level: 1' in acl.lower(), 'ACL did not inherit'
    STAGE.joinpath('p2probe-source').write_text('must not overwrite')
    os.chown(str(STAGE/'p2probe-source'),pwd.getpwnam('p2probepkg').pw_uid,pwd.getpwnam('p2probepkg').pw_gid)
    check('no overwrite',call('files.write',p),'WRITE_FAILED');assert out.read_text()=='P2 executor content\n'
    check('other home',call('files.write',dict(p,dest_dir='/homes/p2probe2')),'SCOPE_DENIED')
    home=pathlib.Path('/var/services/homes/p2probe1');(home/'p2probe-escape').symlink_to('/var/services/homes/p2probe2',target_is_directory=True)
    check('symlink escape',call('files.write',dict(p,dest_dir='/home/p2probe-escape')),'SCOPE_DENIED')
    check('cross job',call('files.write',dict(p,job_id='other')),'DELEGATION_DENIED')
    check('admin runner',call('delegate.create',{'job_id':'j','ops':['files.list'],'scope':'home','path':'/home','ttl_seconds':30},admin),'INELIGIBLE')
    check('root.run admin',call('root.run',{'name':'whoami'},admin))
    check('root.run regular',call('root.run',{'name':'whoami'},alice),'ADMIN_REQUIRED')
    check('files.list',call('files.list',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home'}))
    check('files.info',call('files.info',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home/p2probe-output'}))
    check('files.createFolder',call('files.createFolder',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home','name':'p2probe-folder'}))
    check('files.checkPermission',call('files.checkPermission',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home'}))
    # Mutate only disposable identities/privileges, then restore and read back.
    r=run_api('SYNO.Core.User','set',name='p2probe1',expired='now');assert r.get('success')
    check('disabled runner',call('files.list',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home'}),'INELIGIBLE')
    assert run_api('SYNO.Core.User','set',name='p2probe1',expired='normal').get('success')
    rules=[{'entity_type':'user','entity_name':'p2probe1','app_id':'SYNO.SDS.App.FileStation3.Instance','deny_ip':['0.0.0.0']}]
    assert run_api('SYNO.Core.AppPriv.Rule','set',rules=rules).get('success')
    check('AppPriv deny',call('files.list',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home'}),'APP_DENIED')
    assert run_api('SYNO.Core.AppPriv.Rule','delete',rules=[{'entity_type':'user','entity_name':'p2probe1','app_id':'SYNO.SDS.App.FileStation3.Instance'}]).get('success')
    check('revoke',call('delegate.revoke',{'delegation_id':delegation,'job_id':'p2probejob'}))
    check('revoked',call('files.list',{'delegation_id':delegation,'job_id':'p2probejob','path':'/home'}),'DELEGATION_DENIED')
    # Deleted account with a root-only delegation: success-empty DSM data is insufficient.
    d=create(bob)
    subprocess.run(['/usr/syno/sbin/synouser','--del','p2probe2'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    check('deleted runner',call('files.list',{'delegation_id':d,'job_id':'p2probejob','path':'/home'}),'INELIGIBLE')
finally:
    for session in sessions.values():
        sid=session['sid']
        try:api({'api':'SYNO.API.Auth','version':'7','method':'logout','_sid':sid})
        except Exception:pass
