#!/usr/bin/env python3
"""Disposable Linux SSH fixture; credentials remain in the private state directory."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess


def run(*args, **kw):
    return subprocess.run(args, check=True, text=True, **kw)


def main():
    p = argparse.ArgumentParser()
    p.add_argument('action', choices=['setup', 'metadata', 'teardown'])
    p.add_argument('--state', required=True, type=Path)
    p.add_argument('--name', default='devsy-1621-sshd')
    p.add_argument('--image', default='ghcr.io/devsy-org/test-images/base@sha256:4bcb1b466771b1ba1ea110e2a27daea2f6093f9527fb75ee59703ec89b5561cb')
    p.add_argument('--reuse', action='store_true')
    a = p.parse_args()
    if a.action == 'teardown':
        run('docker', 'rm', '-f', a.name)
        print('Container removed; delete private state after workers finish.')
        return
    if a.action == 'metadata':
        run('docker', 'exec', a.name, 'sh', '-c', "uname -sm; /usr/sbin/sshd -V 2>&1; bash --version | head -1; zsh --version; fish --version; busybox | head -1; readlink -f /bin/sh; getent passwd fixture-bash fixture-zsh fixture-fish fixture-dash fixture-ash; /usr/sbin/sshd -T -f /tmp/fixture-sshd.conf | grep -E '^(passwordauthentication|pubkeyauthentication|permitrootlogin|usepam|printmotd|authorizedkeysfile)'")
        return
    a.state.mkdir(parents=True, exist_ok=True)
    os.chmod(a.state, 0o700)
    key = a.state / 'id_ed25519'
    if not key.exists():
        run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key))
    if not a.reuse:
        run('docker', 'run', '-d', '--pull=never', '--name', a.name, '-p', '127.0.0.1::22', a.image, 'sleep', 'infinity')
        run('docker', 'exec', a.name, 'sh', '-c', 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server bash zsh fish dash busybox python3 sudo')
    run('docker', 'cp', str(key) + '.pub', a.name + ':/tmp/fixture.pub')
    run('docker', 'exec', a.name, 'sh', '-ec', '''
mkdir -p /run/sshd
ln -sf /bin/busybox /bin/ash
for shell in bash zsh fish dash ash; do
  name=fixture-$shell
  id "$name" >/dev/null 2>&1 || useradd -m -s /bin/$shell "$name"
  usermod -p '*' "$name"
  mkdir -p /home/$name/.ssh
  cp /tmp/fixture.pub /home/$name/.ssh/authorized_keys
  chown -R $name:$name /home/$name/.ssh
  chmod 700 /home/$name/.ssh
  chmod 600 /home/$name/.ssh/authorized_keys
done
printf '%s\\n' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no' 'PermitRootLogin no' 'UsePAM no' 'PrintMotd no' 'AuthorizedKeysFile .ssh/authorized_keys' 'PidFile /run/fixture-sshd.pid' 'Subsystem sftp internal-sftp' > /tmp/fixture-sshd.conf
ssh-keygen -A
/usr/sbin/sshd -f /tmp/fixture-sshd.conf -E /tmp/fixture-sshd.log
''')
    port = run('docker', 'port', a.name, '22/tcp', capture_output=True).stdout.strip().split(':')[-1]
    config = a.state / 'ssh_config'
    shells = ['bash', 'zsh', 'fish', 'dash', 'ash']
    config.write_text('Host fixture-*\n  HostName 127.0.0.1\n  Port ' + port + '\n  IdentityFile ' + str(key) + '\n  IdentitiesOnly yes\n  UserKnownHostsFile /dev/null\n  StrictHostKeyChecking no\n  BatchMode yes\n  LogLevel ERROR\n  ConnectTimeout 5\n' + ''.join('Host fixture-' + s + '\n  User fixture-' + s + '\n' for s in shells))
    bindir = a.state / 'bin'
    bindir.mkdir(exist_ok=True)
    for command in ['ssh', 'scp']:
        wrapper = bindir / command
        wrapper.write_text('#!/bin/sh\nexec /usr/bin/' + command + ' -F ' + shlex.quote(str(config)) + ' "$@"\n')
        wrapper.chmod(0o700)
    (a.state / 'fixture.json').write_text(json.dumps({'container': a.name, 'port': int(port), 'config': str(config), 'bin': str(bindir)}, indent=2) + '\n')
    print(json.dumps({'port': int(port), 'state': str(a.state), 'hosts': ['fixture-' + s for s in shells]}))


if __name__ == '__main__':
    main()
