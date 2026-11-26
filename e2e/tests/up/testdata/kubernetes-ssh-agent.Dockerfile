FROM alpine:3.22
RUN apk add --no-cache git openssh-server && \
    adduser -D git && echo 'git:unused-fixture-password' | chpasswd && \
    mkdir -p /home/git/.ssh /srv /run/sshd && \
    printf '%s\n' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no' \
      'PermitRootLogin no' 'AllowUsers git' 'UsePAM no' 'LogLevel VERBOSE' \
      'AuthorizedKeysFile .ssh/authorized_keys' > /etc/ssh/sshd_config
COPY repository /srv/source
COPY authorized_keys /home/git/.ssh/authorized_keys
RUN git clone --bare /srv/source /srv/repository.git && \
    rm -rf /srv/source && chown -R git:git /home/git /srv/repository.git && \
    chmod 700 /home/git/.ssh && chmod 600 /home/git/.ssh/authorized_keys
CMD ["sh", "-c", "ssh-keygen -A && exec /usr/sbin/sshd -D -e"]
