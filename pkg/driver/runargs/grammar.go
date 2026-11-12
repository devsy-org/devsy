package runargs

import "strings"

var dockerCompatibleOptions = map[optionKind]string{
	switchOption: "-d --detach -i --interactive -P --publish-all -q --quiet -t --tty " +
		"--disable-content-trust --env-host --http-proxy --init --no-healthcheck " +
		"--no-hostname --no-hosts --oom-kill-disable --passwd --privileged --read-only " +
		"--read-only-tmpfs --replace --rm --rmi --sig-proxy --tls-verify " +
		"--unsetenv-all --use-api-socket",
	valueOption: "-a --attach -c --cpu-shares -e --env -h --hostname -m --memory " +
		"-p --publish -u --user -v --volume -w --workdir --add-host --annotation --arch " +
		"--authfile --blkio-weight --blkio-weight-device --cap-add --cap-drop --cert-dir " +
		"--cgroup-conf --cgroup-parent --cgroupns --cgroups --chrootdirs --cidfile " +
		"--conmon-pidfile --cosign-certificate-identity " +
		"--cosign-certificate-identity-regexp --cosign-certificate-oidc-issuer " +
		"--cosign-certificate-oidc-issuer-regexp --cosign-key --cpu-count --cpu-percent " +
		"--cpu-period --cpu-quota --cpu-rt-period --cpu-rt-runtime --cpus --cpuset-cpus " +
		"--cpuset-mems --creds --decryption-key --detach-keys --device " +
		"--device-cgroup-rule --device-read-bps --device-read-iops --device-write-bps " +
		"--device-write-iops --dns --dns-opt --dns-option --dns-search --domainname " +
		"--entrypoint --env-file --env-merge --expose --gidmap --gpus --group-add " +
		"--group-entry --health-cmd --health-interval --health-log-destination " +
		"--health-max-log-count --health-max-log-size --health-on-failure " +
		"--health-retries --health-start-interval --health-start-period " +
		"--health-startup-cmd --health-startup-interval --health-startup-retries " +
		"--health-startup-success --health-startup-timeout --health-timeout --hosts-file " +
		"--hostuser --image-volume --init-binary --init-path --io-maxbandwidth " +
		"--io-maxiops --ip --ip6 --ipc --ipfs-address --isolation --kernel-memory --link " +
		"--link-local-ip --log-driver --log-opt --mac-address --memory-reservation " +
		"--memory-swap --memory-swappiness --mount --name --net --net-alias --network " +
		"--network-alias --oom-score-adj --os --passwd-entry --personality --pid " +
		"--pidfile --pids-limit --platform --pod --pod-id-file --preserve-fd " +
		"--preserve-fds --pull --rdt-class --requires --restart --retry --retry-delay " +
		"--runtime --sdnotify --seccomp-policy --secret --security-opt --shm-size " +
		"--shm-size-systemd --signature-policy --stop-signal --stop-timeout --storage-opt " +
		"--subgidname --subuidname --sysctl --systemd --timeout --tmpfs --tz --uidmap " +
		"--ulimit --umask --unsetenv --userns --uts --variant --verify --volume-driver " +
		"--volumes-from",
	labelOption:       "-l --label",
	labelFileOption:   "--label-file",
	imageSourceOption: "--rootfs",
}

var appleContainerOptions = map[optionKind]string{
	switchOption: "-d --detach -i --interactive -t --tty --debug --init --no-dns " +
		"--read-only --remove --rm --rosetta --ssh --virtualization",
	valueOption: "-a --arch -c --cpus -e --env -k --kernel -m --memory -p --publish " +
		"-u --user -v --volume -w --workdir --cwd --cap-add --cap-drop --cidfile --dns " +
		"--dns-domain --dns-option --dns-search --entrypoint --env-file --gid " +
		"--init-image --kernel-arg --masked-path --max-concurrent-downloads --mount --name " +
		"--network --os --platform --progress --publish-socket --read-only-path --runtime " +
		"--scheme --shm-size --tmpfs --uid --ulimit",
	labelOption: "-l --label",
}

var (
	DockerCompatible = newGrammar(true, dockerCompatibleOptions)
	AppleContainer   = newGrammar(false, appleContainerOptions)
)

func newGrammar(attachedShortValues bool, names map[optionKind]string) Grammar {
	options := make(map[string]optionKind)
	for kind, list := range names {
		for name := range strings.FieldsSeq(list) {
			options[name] = kind
		}
	}
	return Grammar{options: options, attachedShortValues: attachedShortValues}
}
