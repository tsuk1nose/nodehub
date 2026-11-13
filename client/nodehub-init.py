#!/usr/bin/env python3
"""Debian first-boot registration. Telemetry is deliberately not implemented."""
import argparse
import fcntl
import json
import hashlib
import os
import pwd
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_STATE = Path('/var/lib/nodehub-init')

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError('Credential redirects are not allowed; check the center URL')

def atomic_write(path, content, mode=0o600):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(dir=path.parent, prefix='.pending-')
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'w') as f:
            f.write(content)
            f.flush()
            os.fsync(f.fileno())
        os.replace(temp, path)
        d = os.open(path.parent, os.O_DIRECTORY)
        try: os.fsync(d)
        finally: os.close(d)
    finally:
        if os.path.exists(temp): os.unlink(temp)

def atomic_json(path, value):
    atomic_write(path, json.dumps(value))

def load(path):
    return json.loads(Path(path).read_text())

def run(argv, env=None):
    # Never echo commands or output that could contain credentials.
    p = subprocess.run(argv, text=True, capture_output=True, env=env)
    if p.returncode:
        raise RuntimeError(f'{Path(argv[0]).name} failed with exit code {p.returncode}; check the service locally')
    return p.stdout

def trusted_hook(path):
    p = Path(path)
    if not p.is_absolute() or not p.is_file():
        raise RuntimeError('The hook must exist and use an absolute file path')
    st = p.stat()
    if st.st_uid != 0 or st.st_mode & 0o022 or not os.access(p, os.X_OK):
        raise RuntimeError('The hook must be executable, owned by root, and not writable by other users')
    return str(p)

class Client:
    def __init__(self, state):
        self.state = state
        self.opener = urllib.request.build_opener(NoRedirect())
        u = urllib.parse.urlparse(state['server'])
        if u.scheme != 'https' or not u.hostname or u.username or u.password or u.query or u.fragment:
            raise RuntimeError('The center URL must use HTTPS without credentials, query parameters, or fragments')
    def request(self, path, body=None, auth=True):
        headers = {'Content-Type': 'application/json', 'X-NodeHub-Request': '1'}
        if auth: headers['Authorization'] = 'Bearer ' + self.state['credential']
        req = urllib.request.Request(self.state['server'].rstrip('/') + '/api' + path,
            data=None if body is None else json.dumps(body).encode(), headers=headers)
        try:
            with self.opener.open(req, timeout=40) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            raise RuntimeError(f'The center returned HTTP {e.code}; check authorization, bindings, and upstream configuration') from None
        except urllib.error.URLError:
            raise RuntimeError('Cannot securely connect to the center; check the network, system clock, and certificates') from None
    def stage(self, value):
        self.request('/node/' + self.state['serial'] + '/stage', {'stage': value, 'error': ''})


def checked_directory(path, mode):
    if path.is_symlink(): raise RuntimeError('SSH directories must not be symbolic links')
    path.mkdir(mode=mode, parents=False, exist_ok=True)
    st = path.stat()
    if not stat.S_ISDIR(st.st_mode) or st.st_uid != 0:
        raise RuntimeError('SSH directories must be owned by root')
    os.chmod(path, mode)

def provision_ssh(client, state, state_dir):
    # Derive the public key from the actual SSHD private host key, never trust a
    # copied .pub file. Missing keys are generated only on the cloned device.
    run(['ssh-keygen', '-A'])
    run(['systemctl', 'enable', '--now', 'ssh.service'])
    run(['systemctl', 'reload', 'ssh.service'])
    host = run(['ssh-keygen', '-y', '-f', '/etc/ssh/ssh_host_ed25519_key']).strip()
    body = {'host_key': host, 'username': 'root'}
    target = client.request('/node/' + state['serial'] + '/ssh-provision', body)
    if target.get('username') != 'root': raise RuntimeError('Unexpected SSH login user returned; update the center')
    public = target.get('authorized_key', '')
    if not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/]+={0,2}', public):
        raise RuntimeError('The center returned an invalid SSH authorized key')
    marker = state_dir / 'ssh-provision.json'
    saved = load(marker) if marker.exists() else {'serial': state['serial'], 'authorized_key': public}
    if saved.get('serial') != state['serial'] or saved.get('authorized_key') != public:
        raise RuntimeError('SSH identity mismatch; refusing to replace it')
    if saved.get('uid', 0) != 0:
        saved['legacy_uid'] = saved['uid']
    account = pwd.getpwnam('root')
    if account.pw_uid != 0: raise RuntimeError('The root account must have UID zero')
    home = Path(account.pw_dir)
    if not home.is_absolute(): raise RuntimeError('The root home directory must use an absolute path')
    saved.update(username='root', uid=0, confirmed=False)
    atomic_json(marker, saved)
    checked_directory(home, 0o700)
    checked_directory(home / '.ssh', 0o700)
    authorized = home / '.ssh' / 'authorized_keys'
    if authorized.is_symlink(): raise RuntimeError('The authorized keys file must not be a symbolic link')
    content = authorized.read_text() if authorized.exists() else ''
    begin = '# BEGIN NODEHUB ' + state['serial']
    end = '# END NODEHUB ' + state['serial']
    block = begin + '\nno-agent-forwarding,no-port-forwarding,no-X11-forwarding ' + public + '\n' + end + '\n'
    if begin in content or end in content:
        pattern = re.compile(r'^' + re.escape(begin) + r'\n.*?^' + re.escape(end) + r'(?:\n|$)', re.M | re.S)
        matches = list(pattern.finditer(content))
        if len(matches) != 1 or content.count(begin) != 1 or content.count(end) != 1:
            raise RuntimeError('The NodeHub authorization block is malformed; refusing to overwrite SSH authorizations')
        content = pattern.sub(lambda _: block, content, count=1)
    else:
        content = content + ('\n' if content and not content.endswith('\n') else '') + block
    atomic_write(authorized, content)
    # Debian normally already permits root public-key access. Only add an
    # override when needed; do not broaden password authentication.
    command = ['/usr/sbin/sshd', '-T', '-C', 'user=root,host=localhost,addr=127.0.0.1']
    effective = run(command)
    policies = dict(line.split(None, 1) for line in effective.splitlines() if len(line.split(None, 1)) == 2)
    allowed = ('yes', 'prohibit-password', 'without-password')
    if policies.get('permitrootlogin') not in allowed:
        dropin = Path('/etc/ssh/sshd_config.d/00-nodehub-root.conf')
        if dropin.is_symlink(): raise RuntimeError('The SSH configuration must not be a symbolic link')
        desired = 'PermitRootLogin prohibit-password\n'
        if dropin.exists() and dropin.read_text() != desired:
            raise RuntimeError('Unexpected existing NodeHub SSH configuration; refusing to overwrite it')
        atomic_write(dropin, desired)
        effective = run(command)
        policies = dict(line.split(None, 1) for line in effective.splitlines() if len(line.split(None, 1)) == 2)
    run(['/usr/sbin/sshd', '-t'])
    if policies.get('permitrootlogin') not in allowed or policies.get('pubkeyauthentication') != 'yes':
        raise RuntimeError('The effective sshd configuration disallows root public-key login; check Include, Match, and earlier rules')
    run(['systemctl', 'reload', 'ssh.service'])
    run(['sync'])
    client.request('/node/' + state['serial'] + '/ssh-provision/confirm', body)
    saved['confirmed'] = True
    atomic_json(marker, saved)
    # Retire only the exact key file previously installed by this tool. Do not
    # remove the old account, its data, or administrator-added authorizations.
    if 'legacy_uid' in saved:
        try: legacy = pwd.getpwnam('nodehub-maint')
        except KeyError: legacy = None
        old_home = Path('/var/lib/nodehub-maint')
        old_authorized = old_home / '.ssh' / 'authorized_keys'
        expected = 'no-agent-forwarding,no-port-forwarding,no-X11-forwarding ' + public + '\n'
        if legacy and legacy.pw_uid == saved['legacy_uid'] and legacy.pw_dir == str(old_home):
            if not old_home.is_symlink() and not (old_home / '.ssh').is_symlink() and not old_authorized.is_symlink() and old_authorized.exists():
                if old_authorized.stat().st_uid == 0 and old_authorized.read_text() == expected:
                    old_authorized.unlink()


def prepare_mesh(client, state):
    # Management credentials stay on the center. Download URLs are returned only
    # after this node proves its independent registration credential.
    import platform
    arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine(), '')
    metadata = client.request('/node/' + state['serial'] + '/bootstrap?arch=' + arch)
    folder = Path('/opt/nodehub/bootstrap')
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(folder, 0o700)
    opener = urllib.request.build_opener(NoRedirect())
    for field, filename, limit in [('binary_url', 'meshagent', 64 * 1024 * 1024), ('settings_url', 'meshagent.msh', 256 * 1024)]:
        url = urllib.parse.urlsplit(metadata[field])
        if url.scheme != 'https' or not url.hostname or url.username or url.password or url.fragment:
            raise RuntimeError('MeshAgent downloads must use HTTPS without embedded credentials')
        with opener.open(metadata[field], timeout=90) as response:
            content = response.read(limit + 1)
        if not content or len(content) > limit: raise RuntimeError('Invalid MeshAgent download size')
        if field == 'binary_url' and not content.startswith(b'\x7fELF'):
            raise RuntimeError('MeshAgent download is not a Linux ELF binary; check the public download entry')
        fd, name = tempfile.mkstemp(dir=folder)
        try:
            with os.fdopen(fd, 'wb') as stream:
                stream.write(content); stream.flush(); os.fsync(stream.fileno())
            os.replace(name, folder / filename)
        finally:
            if os.path.exists(name): os.unlink(name)
    atomic_json('/etc/nodehub/mesh-install.json', {
        'binary': str(folder / 'meshagent'), 'settings': str(folder / 'meshagent.msh'),
        'sha256': hashlib.sha256((folder / 'meshagent').read_bytes()).hexdigest(),
    })

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--activation', default='/etc/nodehub/activation.json')
    ap.add_argument('--state-dir', type=Path, default=DEFAULT_STATE)
    ap.add_argument('--agent-config', help='Optional: enroll and enable the preinstalled native terminal Agent; enable terminal support on the center first')
    ap.add_argument('--auto-mesh', action='store_true', help='Download MeshAgent and group settings through authenticated bootstrap metadata')
    ap.add_argument('--mesh-install-hook', help='Trusted local installer; receives the serial through NODEHUB_SERIAL')
    ap.add_argument('--finalize', action='store_true', help='Run the detached cleanup and reboot phase')
    ap.add_argument('--auto-finalize', action='store_true', help='Automatically bind MeshAgent, clean up, enable overlayroot, and reboot')
    ap.add_argument('--overlay-hook', help='Reviewed local script that prepares OverlayFS for the next boot without rebooting')
    ap.add_argument('--confirm-after-reboot', action='store_true', help='Mark initialization complete after verifying OverlayFS following reboot')
    args = ap.parse_args()
    if os.geteuid() != 0: raise RuntimeError('Run as root')
    args.state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(args.state_dir, 0o700)
    with (args.state_dir / 'lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state_file = args.state_dir / 'state.json'
        if state_file.exists():
            state = load(state_file)
        else:
            if run(['findmnt', '-n', '-o', 'FSTYPE', '/']).strip() == 'overlay':
                raise RuntimeError('The root filesystem already uses OverlayFS; refusing to create an ephemeral device identity')
            activation = load(args.activation)
            state = {'server': activation['server'], 'request_id': secrets.token_hex(32), 'credential': secrets.token_hex(32)}
            atomic_json(state_file, state)  # Persist before any request; response loss must not duplicate registration.
        c = Client(state)
        if 'serial' not in state:
            token = load(args.activation)['token'] if Path(args.activation).exists() else ''
            result = c.request('/register', {'token': token, 'request_id': state['request_id'], 'credential': state['credential']}, auth=False)
            if not re.fullmatch('[A-Z0-9]{12}', result['serial']): raise RuntimeError('The center returned an invalid serial')
            state['serial'] = result['serial']
            atomic_json(state_file, state)
        # Verify independently authenticated recovery before deleting the bootstrap secret.
        details = c.request('/node/' + state['serial'])
        Path(args.activation).unlink(missing_ok=True)
        if args.confirm_after_reboot:
            if not state.get('finalizing'): raise RuntimeError('Finalization has not been requested')
            if run(['findmnt', '-n', '-o', 'FSTYPE', '/']).strip() != 'overlay':
                raise RuntimeError('The root filesystem is not OverlayFS; cannot mark initialization complete')
            c.stage('complete')
            print('Root OverlayFS and upstream bindings verified; initialization complete.')
            return
        if args.finalize:
            if run(['findmnt', '-n', '-o', 'FSTYPE', '/']).strip() == 'overlay':
                raise RuntimeError('Finalization requires a persistent root filesystem; enter maintenance mode first')
            n = details['node']
            if not n['headscale_id'] or not n['mesh_id']:
                raise RuntimeError('Bind both upstream devices before finalizing')
            if not args.overlay_hook: raise RuntimeError('A device-validated --overlay-hook is required')
            hook = trusted_hook(args.overlay_hook)
            # Refuse destructive cleanup when a transient installation only
            # appeared online once. Both executable and identity must survive.
            if not os.path.ismount('/data'):
                raise RuntimeError('/data is not mounted; refusing cleanup')
            mesh_dir = Path('/data/nodehub/meshagent')
            for name in ('meshagent', 'meshagent.msh', 'meshagent.db'):
                if not (mesh_dir / name).is_file():
                    raise RuntimeError('Persistent MeshAgent installation is incomplete; refusing cleanup')
            command = run(['systemctl', 'show', 'meshagent.service', '-p', 'ExecStart', '--value'])
            if 'path=' + str(mesh_dir / 'meshagent') + ' ' not in command:
                raise RuntimeError('MeshAgent service points outside its persistent installation; refusing cleanup')
            run(['systemctl', 'is-active', '--quiet', 'meshagent.service'])
            # A detached systemd unit survives stopping the first-boot service.
            c.stage('awaiting_overlay')
            state['finalizing'] = True
            atomic_json(state_file, state)
            # Validate and prepare the next boot before removing recovery tools.
            # This only writes configuration; root remains persistent until reboot.
            run([hook])
            service = Path('/etc/systemd/system/nodehub-init.service')
            if service.exists():
                run(['systemctl', 'disable', '--now', 'nodehub-init.service'])
                service.unlink(missing_ok=True)
            Path('/etc/systemd/system/nodehub-init.service.d/agent.conf').unlink(missing_ok=True)
            Path('/etc/systemd/system/nodehub-init.service.d/bootstrap.conf').unlink(missing_ok=True)
            overrides = Path('/etc/systemd/system/nodehub-init.service.d')
            if overrides.exists() and not any(overrides.iterdir()): overrides.rmdir()
            run(['systemctl', 'daemon-reload'])
            Path(args.activation).unlink(missing_ok=True)
            # Remove installed initialization entrypoint; source/recovery copy is external to the image.
            installed = Path('/usr/local/sbin/nodehub-init')
            if installed.resolve() == Path(__file__).resolve(): installed.unlink(missing_ok=True)
            # Enable the next-boot overlay only after shared secrets and startup entrypoints are gone.
            # If interrupted earlier, the existing remote channels can restore this tool and retry.
            run(['sync'])
            import shutil
            # Remove only files owned by the bootstrap workflow, preserving all
            # installed agents, SSH authorizations, and their runtime identities.
            for name in ('/etc/nodehub/mesh-install.json', '/usr/local/lib/nodehub/mesh-install',
                         '/usr/local/lib/nodehub/enable-overlayroot'):
                Path(name).unlink(missing_ok=True)
            if Path('/opt/nodehub/bootstrap').exists():
                shutil.rmtree(Path('/opt/nodehub/bootstrap'))
            shutil.rmtree(args.state_dir)
            run(['sync'])
            print('Bootstrap entrypoints and credentials removed; rebooting. Verify through an existing remote channel after reboot.')
            run(['systemctl', 'reboot'])
            return
        if state.get('finalizing') and not args.auto_finalize:
            raise RuntimeError('Finalization is in progress; resume with --finalize or confirm after reboot instead of registering again')
        if run(['findmnt', '-n', '-o', 'FSTYPE', '/']).strip() == 'overlay':
            raise RuntimeError('First registration requires a persistent writable root filesystem, not ephemeral OverlayFS')
        if args.agent_config and not args.finalize and not args.confirm_after_reboot:
            account = pwd.getpwnam('nodehub-agent')
            agent_config = load(args.agent_config)
            identity_path = Path(agent_config['identity_file'])
            if identity_path != Path('/var/lib/nodehub-agent/identity.json'):
                raise RuntimeError('Automatic enrollment supports only the default Agent identity path; enroll other layouts manually')
            run(['/usr/local/sbin/nodehub-agent', '-config', args.agent_config, '-enroll', '-bootstrap-state', str(state_file)])
            os.chown(identity_path, account.pw_uid, account.pw_gid)
            os.chmod(identity_path, 0o600)
            provision_ssh(c, state, args.state_dir)
            run(['systemctl', 'enable', '--now', 'nodehub-agent.service'])
            state['agent_enrolled'] = True
            atomic_json(state_file, state)
        run(['hostnamectl', 'set-hostname', state['serial']])
        hosts = Path('/etc/hosts')
        content = hosts.read_text()
        line = '127.0.1.1\t' + state['serial']
        content = re.sub(r'^127\.0\.1\.1\s+.*$', line, content, flags=re.M) if re.search(r'^127\.0\.1\.1\s+', content, re.M) else content.rstrip() + '\n' + line + '\n'
        hosts.write_text(content)
        c.stage('hostname_ready')
        status = json.loads(run(['tailscale', 'status', '--json']))
        if status.get('BackendState') != 'Running':
            provision = c.request('/node/' + state['serial'] + '/headscale-key', {})
            keyfile = args.state_dir / 'tailscale-authkey'
            fd = os.open(keyfile, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            try:
                with os.fdopen(fd, 'w') as f: f.write(provision['key'])
                run(['tailscale', 'up', '--login-server=' + provision['login_server'], '--hostname=' + state['serial'], '--auth-key=file:' + str(keyfile)])
            finally: keyfile.unlink(missing_ok=True)
            status = json.loads(run(['tailscale', 'status', '--json']))
        run(['tailscale', 'set', '--hostname=' + state['serial']])
        key = status.get('Self', {}).get('PublicKey')
        if not key: raise RuntimeError('Tailscale did not return the local node public key')
        c.request('/node/' + state['serial'] + '/headscale-bind', {'node_key': key})
        c.stage('tailscale_ready')
        if not state.get('mesh_installed'):
            if args.auto_mesh: prepare_mesh(c, state)
            if not args.mesh_install_hook: raise RuntimeError('Provide a reviewed MeshAgent installation hook; completed steps have been retained')
            env = dict(os.environ, NODEHUB_SERIAL=state['serial'])
            run([trusted_hook(args.mesh_install_hook)], env=env)
            state['mesh_installed'] = True
            atomic_json(state_file, state)
        c.stage('awaiting_binding')
        if args.auto_finalize:
            if not args.overlay_hook: raise RuntimeError('Automatic finalization requires the overlayroot hook')
            c.request('/node/' + state['serial'] + '/meshcentral-auto-bind', {})
            # Launch another unit: stopping nodehub-init must not kill cleanup.
            command = ['systemd-run', '--unit=nodehub-finalize', '--collect',
                       '--property=Restart=on-failure', '--property=RestartSec=60s',
                       '--property=StartLimitIntervalSec=0',
                       '--on-active=5s', '/usr/local/sbin/nodehub-init', '--finalize',
                       '--state-dir', str(args.state_dir), '--activation', args.activation,
                       '--overlay-hook', trusted_hook(args.overlay_hook)]
            run(command)
            print('Registration complete; automatic cleanup and reboot scheduled.')
        else:
            print('Registration steps completed: ' + state['serial'] + '. Finalization is available with --finalize.')

if __name__ == '__main__':
    try: main()
    except (RuntimeError, OSError, ValueError, KeyError) as e:
        print('Initialization incomplete: ' + str(e), file=sys.stderr)
        sys.exit(1)
