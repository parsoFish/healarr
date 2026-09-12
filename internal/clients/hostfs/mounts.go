package hostfs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// defaultMountinfoPath is the process's own mount namespace view of the
// kernel mount table.
const defaultMountinfoPath = "/proc/self/mountinfo"

// minMountinfoFields is the fewest whitespace-separated fields a mountinfo
// line can have before its " - " separator and trailing fstype/source/
// super-options: mount ID, parent ID, major:minor, root, mount point,
// mount options.
const minMountinfoFields = 6

// OS is a Client backed by the real host: /proc/self/mountinfo and the
// syscall package.
type OS struct {
	mountinfoPath string
}

var _ Client = (*OS)(nil)

// New builds an OS client that reads mountinfoPath for Mounts. An empty
// mountinfoPath defaults to /proc/self/mountinfo.
func New(mountinfoPath string) *OS {
	if mountinfoPath == "" {
		mountinfoPath = defaultMountinfoPath
	}
	return &OS{mountinfoPath: mountinfoPath}
}

// Mounts parses o.mountinfoPath, one Mount per line.
func (o *OS) Mounts(ctx context.Context) ([]Mount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.Open(o.mountinfoPath)
	if err != nil {
		return nil, fmt.Errorf("hostfs: open mountinfo: %w", err)
	}
	defer func() { _ = f.Close() }()

	var mounts []Mount
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		m, err := parseMountLine(line)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, m)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("hostfs: read mountinfo: %w", err)
	}
	return mounts, nil
}

// parseMountLine parses one /proc/self/mountinfo line, e.g.:
//
//	36 35 98:0 / /mnt1 rw,noatime shared:1 - ext3 /dev/root rw,errors=continue
//
// Fields up to the mount options are fixed-position; a variable number of
// optional fields follow, terminated by a lone "-", after which come
// fstype, mount source and super-options.
func parseMountLine(line string) (Mount, error) {
	fields := strings.Fields(line)
	if len(fields) < minMountinfoFields {
		return Mount{}, fmt.Errorf("hostfs: malformed mountinfo line (too few fields): %q", line)
	}

	sepIdx := -1
	for i := minMountinfoFields; i < len(fields); i++ {
		if fields[i] == "-" {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 || sepIdx+3 >= len(fields) {
		return Mount{}, fmt.Errorf("hostfs: malformed mountinfo line (missing \" - \" separator): %q", line)
	}

	return Mount{
		Target:  unescapeOctal(fields[4]),
		Source:  unescapeOctal(fields[sepIdx+2]),
		FSType:  fields[sepIdx+1],
		Options: strings.Split(fields[5], ","),
	}, nil
}

// IsMountpoint reports whether path is itself a mount point. "/" is always
// a mount point; otherwise path is a mount point when its device differs
// from its parent directory's device.
func (o *OS) IsMountpoint(ctx context.Context, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if path == "/" {
		return true, nil
	}
	dev, err := o.DeviceID(ctx, path)
	if err != nil {
		return false, err
	}
	parentDev, err := o.DeviceID(ctx, filepath.Dir(path))
	if err != nil {
		return false, err
	}
	return dev != parentDev, nil
}

// DeviceID returns path's underlying device number (stat st_dev).
func (o *OS) DeviceID(ctx context.Context, path string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("hostfs: stat %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("hostfs: stat %s: unsupported platform (no syscall.Stat_t)", path)
	}
	return stat.Dev, nil
}

// unescapeOctal decodes mountinfo's octal escapes (\NNN, e.g. \040 for a
// space) in-place. Bytes that don't form a valid escape are copied as-is.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
