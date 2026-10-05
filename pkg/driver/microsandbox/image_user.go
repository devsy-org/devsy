package microsandbox

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/image"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const (
	maxAccountFileSize = 4 << 20
	rootUser           = "root"
	passwdPath         = "etc/passwd"
	groupPath          = "etc/group"
)

type imageUserIdentity struct {
	name, group            string
	uid, gid               uint32
	numericUID, numericGID bool
}

type imageUserResolver interface {
	Resolve(ctx context.Context, image string, builtLocally bool, user string) (*mountOwner, error)
}

type filesystemUserResolver struct{ dockerPath string }

func (r filesystemUserResolver) Resolve(
	ctx context.Context,
	ref string,
	builtLocally bool,
	user string,
) (*mountOwner, error) {
	if owner, complete, err := explicitOwner(user); complete || err != nil {
		return owner, err
	}
	var img v1.Image
	var err error
	if builtLocally {
		var cleanup func()
		img, cleanup, err = r.localImage(ctx, ref)
		if err != nil {
			return nil, err
		}
		defer cleanup()
	} else {
		img, err = image.GetImageForArch(ctx, ref, runtime.GOARCH)
		if err != nil {
			return nil, err
		}
	}
	return ownerFromImage(ctx, img, user)
}

// The archive must remain available until lazy layer reads finish.
func (r filesystemUserResolver) localImage(
	ctx context.Context,
	ref string,
) (v1.Image, func(), error) {
	archive, err := os.CreateTemp("", "devsy-msb-owner-*.tar")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.Remove(archive.Name()) }
	docker := r.dockerPath
	if docker == "" {
		docker = "docker"
	}
	// #nosec G204 -- configured Docker-compatible executable, fixed subcommand, image argument
	cmd := exec.CommandContext(ctx, docker, "save", ref)
	cmd.Stdout = archive
	var stderr strings.Builder
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := archive.Close()
	if runErr != nil {
		cleanup()
		return nil, nil, fmt.Errorf(
			"save final local image %q: %s: %w",
			ref,
			strings.TrimSpace(stderr.String()),
			runErr,
		)
	}
	if closeErr != nil {
		cleanup()
		return nil, nil, closeErr
	}
	img, err := tarball.ImageFromPath(archive.Name(), nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("read final local image %q: %w", ref, err)
	}
	return img, cleanup, nil
}

func numericID(value string) (uint32, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false, nil
		}
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, true, fmt.Errorf("invalid numeric account ID %q: %w", value, err)
	}
	return uint32(n), true, nil
}

func parseImageUserIdentity(user string) (imageUserIdentity, error) {
	name, group, hasGroup := strings.Cut(user, ":")
	if name == "" || (hasGroup && (group == "" || strings.Contains(group, ":"))) {
		return imageUserIdentity{}, fmt.Errorf("invalid user identity %q", user)
	}
	uid, numericUID, err := numericID(name)
	if err != nil {
		return imageUserIdentity{}, err
	}
	gid, numericGID, err := numericID(group)
	if err != nil {
		return imageUserIdentity{}, err
	}
	return imageUserIdentity{
		name:       name,
		group:      group,
		uid:        uid,
		gid:        gid,
		numericUID: numericUID,
		numericGID: numericGID,
	}, nil
}

func explicitOwner(user string) (*mountOwner, bool, error) {
	identity, err := parseImageUserIdentity(user)
	if err != nil {
		return nil, false, err
	}
	if identity.group == "" {
		if identity.name == rootUser || (identity.numericUID && identity.uid == 0) {
			return &mountOwner{}, true, nil
		}
	}
	if identity.numericUID && identity.numericGID {
		return &mountOwner{UID: identity.uid, GID: identity.gid}, true, nil
	}
	return nil, false, nil
}

func ownerFromImage(ctx context.Context, img v1.Image, user string) (*mountOwner, error) {
	if owner, complete, err := explicitOwner(user); complete || err != nil {
		return owner, err
	}
	accounts, err := imageAccountFiles(ctx, img)
	if err != nil {
		return nil, err
	}
	return ownerFromAccounts(user, accounts[passwdPath], accounts[groupPath])
}

func imageAccountFiles(ctx context.Context, img v1.Image) (map[string]string, error) {
	stream := mutate.Extract(img)
	defer func() { _ = stream.Close() }()
	accounts := map[string]string{}
	reader := tar.NewReader(stream)
	for len(accounts) < 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read image accounts: %w", err)
		}
		file := path.Clean(strings.TrimPrefix(header.Name, "/"))
		if file != passwdPath && file != groupPath {
			continue
		}
		accounts[file], err = readAccountFile(reader, header)
		if err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

func readAccountFile(reader io.Reader, header *tar.Header) (string, error) {
	if header.Typeflag != tar.TypeReg {
		return "", fmt.Errorf("final image /%s is not a regular account file", header.Name)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxAccountFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read image /%s: %w", header.Name, err)
	}
	if len(data) > maxAccountFileSize {
		return "", fmt.Errorf("image /%s exceeds account file size limit", header.Name)
	}
	return string(data), nil
}

func ownerFromAccounts(user, passwd, groups string) (*mountOwner, error) {
	name, group, hasGroup := strings.Cut(user, ":")
	uid, numeric, err := numericID(name)
	if err != nil {
		return nil, err
	}
	owner := &mountOwner{UID: uid}
	// Explicit numeric IDs need no passwd entry when a group is supplied.
	if !numeric || !hasGroup {
		owner, err = passwdOwner(passwd, name, uid, numeric)
		if err != nil {
			return nil, err
		}
	}
	if hasGroup {
		owner.GID, err = groupID(groups, group)
		if err != nil {
			return nil, err
		}
	}
	return owner, nil
}

func passwdOwner(passwd, name string, uid uint32, numeric bool) (*mountOwner, error) {
	for line := range strings.SplitSeq(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			continue
		}
		if !matchesPasswdRow(fields, name, uid, numeric) {
			continue
		}
		return passwdRowOwner(fields)
	}
	return nil, fmt.Errorf("user %q not found in final image /etc/passwd", name)
}

func matchesPasswdRow(fields []string, name string, uid uint32, numeric bool) bool {
	if !numeric {
		return fields[0] == name
	}
	entryUID, valid, err := numericID(fields[2])
	return valid && err == nil && entryUID == uid
}

func passwdRowOwner(fields []string) (*mountOwner, error) {
	uid, validUID, uidErr := numericID(fields[2])
	gid, validGID, gidErr := numericID(fields[3])
	if uidErr != nil || gidErr != nil || !validUID || !validGID {
		return nil, fmt.Errorf("invalid account IDs for %q in final image /etc/passwd", fields[0])
	}
	return &mountOwner{UID: uid, GID: gid}, nil
}

func groupID(groups, group string) (uint32, error) {
	gid, numeric, err := numericID(group)
	if err != nil || numeric {
		return gid, err
	}
	for line := range strings.SplitSeq(groups, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || fields[0] != group {
			continue
		}
		gid, valid, err := numericID(fields[2])
		if err != nil || !valid {
			return 0, fmt.Errorf("invalid group ID for %q in final image /etc/group", group)
		}
		return gid, nil
	}
	return 0, fmt.Errorf("group %q not found in final image /etc/group", group)
}
