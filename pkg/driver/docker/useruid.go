package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func (d *dockerDriver) UpdateContainerUserUID(
	ctx context.Context,
	workspaceId string,
	parsedConfig *config.DevContainerConfig,
	writer io.Writer,
) error {
	target, err := d.resolveUserUpdate(parsedConfig)
	if err != nil || target == nil {
		return err
	}
	// target.containerUser is guaranteed non-empty by shouldUpdateUserUID

	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	}
	if container == nil {
		return fmt.Errorf("container not found")
	}

	files, info, err := d.updateUserMappings(ctx, &userMappingParams{
		containerID:   container.ID,
		containerUser: target.containerUser,
		localUser:     target.localUser,
		writer:        writer,
	})
	if err != nil {
		return err
	}
	defer files.cleanup()

	if shouldSkipUpdate(target.localUser, info) {
		log.Info("container user UID/GID already match local user, skipping update")
		return nil
	}

	log.Infof("updating container user %q UID from %s to %s and GID from %s to %s",
		target.containerUser, info.Uid, target.localUser.Uid, info.Gid, target.localUser.Gid)

	if err := d.copyFilesToContainer(ctx, container.ID, files, writer); err != nil {
		return err
	}

	return d.applyPermissions(ctx, &applyPermissionsParams{
		containerID:   container.ID,
		localUid:      target.localUser.Uid,
		localGid:      target.localUser.Gid,
		containerHome: info.HomeDir,
		writer:        writer,
	})
}

// userUpdateTarget identifies the local and container users involved in a
// UID/GID update.
type userUpdateTarget struct {
	localUser     *user.User
	containerUser string
}

// resolveUserUpdate decides whether a UID/GID update is warranted and, if so,
// gathers the local and container users. A nil target means the update should
// be skipped (config opt-out or a root local user), in which case the caller
// returns early with a nil error.
func (d *dockerDriver) resolveUserUpdate(
	parsedConfig *config.DevContainerConfig,
) (*userUpdateTarget, error) {
	if !d.shouldUpdateUserUID(parsedConfig) {
		return nil, nil
	}

	localUser, containerUser, err := d.gatherUpdateRequirements(parsedConfig)
	if err != nil {
		return nil, err
	}

	if localUser.Uid == "0" {
		log.Info("local user is root, skipping UID/GID update")
		return nil, nil
	}

	return &userUpdateTarget{localUser: localUser, containerUser: containerUser}, nil
}

func (d *dockerDriver) gatherUpdateRequirements(
	parsedConfig *config.DevContainerConfig,
) (*user.User, string, error) {
	localUser, err := user.Current()
	if err != nil {
		return nil, "", err
	}

	containerUser := d.getContainerUser(parsedConfig)
	return localUser, containerUser, nil
}

type userMappingParams struct {
	containerID   string
	containerUser string
	localUser     *user.User
	writer        io.Writer
}

func (d *dockerDriver) updateUserMappings(
	ctx context.Context,
	params *userMappingParams,
) (*tempFiles, *user.User, error) {
	files, err := d.createTempFiles()
	if err != nil {
		return nil, nil, err
	}

	if err := d.fetchContainerFiles(ctx, params.containerID, files, params.writer); err != nil {
		files.cleanup()
		return nil, nil, err
	}

	info, err := d.processUserFiles(
		files,
		params.containerUser,
		params.localUser.Uid,
		params.localUser.Gid,
	)
	if err != nil {
		files.cleanup()
		return nil, nil, err
	}

	return files, info, nil
}

// shouldSkipUpdate returns true if UID/GID mapping should be skipped.
// localUser is the host system's current user.
// info contains the container user's current UID/GID (parsed from container's /etc/passwd).
func shouldSkipUpdate(localUser *user.User, info *user.User) bool {
	return info.Uid == "0" || (localUser.Uid == info.Uid && localUser.Gid == info.Gid)
}

func (d *dockerDriver) shouldUpdateUserUID(parsedConfig *config.DevContainerConfig) bool {
	isLinux := runtime.GOOS == "linux"
	hasUser := parsedConfig.ContainerUser != "" || parsedConfig.RemoteUser != ""

	var shouldUpdate bool
	switch {
	case parsedConfig.UpdateRemoteUserUID != nil:
		shouldUpdate = *parsedConfig.UpdateRemoteUserUID
	case d.UpdateRemoteUserUIDDefault == "off":
		shouldUpdate = false
	default:
		shouldUpdate = true
	}

	return isLinux && hasUser && shouldUpdate
}

func (d *dockerDriver) getContainerUser(parsedConfig *config.DevContainerConfig) string {
	if parsedConfig.RemoteUser != "" {
		return parsedConfig.RemoteUser
	}
	return parsedConfig.ContainerUser
}

// containerPath formats a `docker cp` operand referring to a path inside a
// container, e.g. "abc123:/etc/passwd".
func containerPath(containerID, path string) string {
	return fmt.Sprintf("%s:%s", containerID, path)
}

// dockerCp runs `docker cp src dst`. Either operand may be a container path
// (see containerPath) or a host path.
func (d *dockerDriver) dockerCp(ctx context.Context, src, dst string, writer io.Writer) error {
	args := []string{"cp", src, dst}
	log.Debugf(
		"copying file: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	return d.Docker.Run(ctx, args, nil, writer, writer)
}

type lineProcessor func(line string, fields []string) (modifiedLine string, shouldWrite bool, err error)

func (d *dockerDriver) processColonDelimitedFile(
	in *os.File,
	out *os.File,
	fieldCount int,
	processor lineProcessor,
) error {
	scanner := bufio.NewScanner(in)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.SplitN(line, ":", fieldCount)

		if len(fields) < fieldCount {
			if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
				return err
			}
			continue
		}

		modifiedLine, shouldWrite, err := processor(line, fields)
		if err != nil {
			return err
		}

		if shouldWrite {
			if _, err := fmt.Fprintf(out, "%s\n", modifiedLine); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}

type passwordFileUpdateParams struct {
	passwdIn      *os.File
	passwdOut     *os.File
	containerUser string
	localUid      string
	localGid      string
}

// updatePasswdFile processes /etc/passwd, replacing the target user's UID/GID with local values.
// It reads each line from passwdIn, and for lines matching containerUser, extracts the original
// UID, GID, and home directory, then writes a modified entry with localUid and localGid to passwdOut.
// All other lines are copied unchanged. Returns userInfo with the original container values, or an
// error if the user is not found in the passwd file.
func (d *dockerDriver) updatePasswdFile(params *passwordFileUpdateParams) (*user.User, error) {
	info := &user.User{}

	// parse passwd format: username:password:uid:gid:gecos:home:shell
	processor := func(line string, fields []string) (string, bool, error) {
		if fields[0] != params.containerUser {
			return "", false, nil
		}

		info.Uid = fields[2]
		info.Gid = fields[3]
		info.HomeDir = fields[5]

		modifiedLine := strings.Join([]string{
			fields[0],
			fields[1],
			params.localUid,
			params.localGid,
			fields[4],
			fields[5],
			fields[6],
		}, ":")
		return modifiedLine, true, nil
	}

	if err := d.processColonDelimitedFile(
		params.passwdIn,
		params.passwdOut,
		7,
		processor,
	); err != nil {
		return nil, err
	}

	if info.Uid == "" {
		return nil, fmt.Errorf("user %q not found in passwd", params.containerUser)
	}

	return info, nil
}

// updateGroupFile processes /etc/group, replacing entries with the target GID to use localGid.
// It reads each line from groupIn, and for lines where the GID field matches containerGid,
// writes a modified entry with localGid to groupOut. All other lines are copied unchanged.
// Returns an error if scanning fails.
func (d *dockerDriver) updateGroupFile(
	groupIn *os.File,
	groupOut *os.File,
	containerGid, localGid string,
) error {
	// parse group format: groupname:password:gid:user_list
	processor := func(line string, fields []string) (string, bool, error) {
		if fields[2] != containerGid {
			return "", false, nil
		}

		modifiedLine := strings.Join([]string{fields[0], fields[1], localGid, fields[3]}, ":")
		return modifiedLine, true, nil
	}

	return d.processColonDelimitedFile(groupIn, groupOut, 4, processor)
}

type applyPermissionsParams struct {
	containerID   string
	localUid      string
	localGid      string
	containerHome string
	writer        io.Writer
}

func (d *dockerDriver) applyPermissions(ctx context.Context, params *applyPermissionsParams) error {
	args := []string{
		dockerExec, "-u", rootUser, params.containerID,
		"chmod", "644", "/etc/passwd", "/etc/group",
	}
	log.Debugf(
		"modifying permissions of /etc/passwd and /etc/group: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	if err := d.Docker.Run(ctx, args, nil, params.writer, params.writer); err != nil {
		return err
	}

	if params.containerHome == "" {
		log.Warnf(
			"container home directory not found, skipping chown: containerID=%s",
			params.containerID,
		)
		return nil
	}

	args = []string{
		dockerExec,
		"-u",
		rootUser,
		params.containerID,
		"chown",
		"-R",
		fmt.Sprintf("%s:%s", params.localUid, params.localGid),
		params.containerHome,
	}
	log.Debugf(
		"running docker chown command: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	return d.Docker.Run(ctx, args, nil, params.writer, params.writer)
}

type tempFiles struct {
	passwdIn  *os.File
	groupIn   *os.File
	passwdOut *os.File
	groupOut  *os.File
}

func (t *tempFiles) cleanup() {
	if t.passwdIn != nil {
		_ = t.passwdIn.Close()
		_ = os.Remove(t.passwdIn.Name())
	}
	if t.groupIn != nil {
		_ = t.groupIn.Close()
		_ = os.Remove(t.groupIn.Name())
	}
	if t.passwdOut != nil {
		_ = t.passwdOut.Close()
		_ = os.Remove(t.passwdOut.Name())
	}
	if t.groupOut != nil {
		_ = t.groupOut.Close()
		_ = os.Remove(t.groupOut.Name())
	}
}

func (d *dockerDriver) createTempFiles() (*tempFiles, error) {
	files := &tempFiles{}
	var err error

	files.passwdIn, err = os.CreateTemp("", pkgconfig.BinaryName+"_container_passwd_in")
	if err != nil {
		return nil, err
	}

	files.groupIn, err = os.CreateTemp("", pkgconfig.BinaryName+"_container_group_in")
	if err != nil {
		files.cleanup()
		return nil, err
	}

	files.passwdOut, err = os.CreateTemp("", pkgconfig.BinaryName+"_container_passwd_out")
	if err != nil {
		files.cleanup()
		return nil, err
	}

	files.groupOut, err = os.CreateTemp("", pkgconfig.BinaryName+"_container_group_out")
	if err != nil {
		files.cleanup()
		return nil, err
	}

	return files, nil
}

func (d *dockerDriver) fetchContainerFiles(
	ctx context.Context,
	containerID string,
	files *tempFiles,
	writer io.Writer,
) error {
	if err := d.dockerCp(
		ctx,
		containerPath(containerID, "/etc/passwd"),
		files.passwdIn.Name(),
		writer,
	); err != nil {
		return err
	}
	return d.dockerCp(ctx, containerPath(containerID, "/etc/group"), files.groupIn.Name(), writer)
}

func (d *dockerDriver) processUserFiles(
	files *tempFiles,
	containerUser, localUid, localGid string,
) (*user.User, error) {
	passwdIn, err := os.Open(files.passwdIn.Name())
	if err != nil {
		return nil, err
	}
	defer func() { _ = passwdIn.Close() }()

	info, err := d.updatePasswdFile(&passwordFileUpdateParams{
		passwdIn:      passwdIn,
		passwdOut:     files.passwdOut,
		containerUser: containerUser,
		localUid:      localUid,
		localGid:      localGid,
	})
	if err != nil {
		return nil, err
	}

	groupIn, err := os.Open(files.groupIn.Name())
	if err != nil {
		return nil, err
	}
	defer func() { _ = groupIn.Close() }()

	return info, d.updateGroupFile(groupIn, files.groupOut, info.Gid, localGid)
}

func (d *dockerDriver) copyFilesToContainer(
	ctx context.Context, containerID string, files *tempFiles, writer io.Writer,
) error {
	if err := d.dockerCp(
		ctx,
		files.passwdOut.Name(),
		containerPath(containerID, "/etc/passwd"),
		writer,
	); err != nil {
		return err
	}
	return d.dockerCp(ctx, files.groupOut.Name(), containerPath(containerID, "/etc/group"), writer)
}
