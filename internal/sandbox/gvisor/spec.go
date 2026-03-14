package gvisor

import (
	"sort"

	ocispec "github.com/opencontainers/runtime-spec/specs-go"
)

// BuildSpec constructs an OCI runtime spec from ContainerOptions.
func BuildSpec(opts ContainerOptions) *ocispec.Spec {
	return &ocispec.Spec{
		Version: "1.0.2-dev",
		Root: &ocispec.Root{
			Path:     opts.RootFS,
			Readonly: opts.ReadOnlyRootFS,
		},
		Process: buildProcess(opts),
		Linux:   buildLinux(opts),
		Mounts:  buildMounts(opts),
	}
}

func buildProcess(opts ContainerOptions) *ocispec.Process {
	env := buildEnv(opts.Env)
	user := ocispec.User{UID: 0, GID: 0}
	if opts.User != nil {
		user.UID = opts.User.UID
		user.GID = opts.User.GID
	}

	return &ocispec.Process{
		Terminal: false,
		User:     user,
		Args:     opts.Command,
		Env:      env,
		Cwd:      opts.WorkDir,
		Capabilities: &ocispec.LinuxCapabilities{
			Bounding:  minimalCaps(),
			Effective: minimalCaps(),
			Permitted: minimalCaps(),
		},
		NoNewPrivileges: true,
		Rlimits: []ocispec.POSIXRlimit{
			{Type: "RLIMIT_NOFILE", Hard: 65536, Soft: 65536},
		},
	}
}

// minimalCaps returns the minimum Linux capabilities needed for builds/tests.
func minimalCaps() []string {
	return []string{
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_FOWNER",
		"CAP_FSETID",
		"CAP_KILL",
		"CAP_SETGID",
		"CAP_SETUID",
		"CAP_SETPCAP",
		"CAP_NET_BIND_SERVICE",
		"CAP_SYS_CHROOT",
	}
}

// buildEnv constructs the environment variable list.
func buildEnv(env map[string]string) []string {
	defaults := map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME": "/root",
		"TERM": "xterm-256color",
		"LANG": "C.UTF-8",
	}
	for k, v := range env {
		defaults[k] = v
	}
	result := make([]string, 0, len(defaults))
	for k, v := range defaults {
		result = append(result, k+"="+v)
	}
	sort.Strings(result)
	return result
}

func buildLinux(opts ContainerOptions) *ocispec.Linux {
	return &ocispec.Linux{
		Namespaces: []ocispec.LinuxNamespace{
			{Type: ocispec.PIDNamespace},
			{Type: ocispec.MountNamespace},
			{Type: ocispec.IPCNamespace},
			{Type: ocispec.UTSNamespace},
			{Type: ocispec.NetworkNamespace},
		},
		Resources: buildCgroupResources(opts.Resources),
	}
}

func buildMounts(opts ContainerOptions) []ocispec.Mount {
	mounts := []ocispec.Mount{
		{
			Destination: "/proc",
			Type:        "proc",
			Source:      "proc",
			Options:     []string{"nosuid", "noexec", "nodev"},
		},
		{
			Destination: "/dev",
			Type:        "tmpfs",
			Source:      "tmpfs",
			Options:     []string{"nosuid", "noexec", "mode=755", "size=65536k"},
		},
		{
			Destination: "/dev/pts",
			Type:        "devpts",
			Source:      "devpts",
			Options:     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620"},
		},
		{
			Destination: "/dev/shm",
			Type:        "tmpfs",
			Source:      "shm",
			Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=67108864"},
		},
		{
			Destination: "/tmp",
			Type:        "tmpfs",
			Source:      "tmpfs",
			Options:     []string{"nosuid", "nodev", "mode=1777"},
		},
		{
			Destination: "/sys",
			Type:        "sysfs",
			Source:      "sysfs",
			Options:     []string{"nosuid", "noexec", "nodev", "ro"},
		},
	}

	for _, m := range opts.ExtraMounts {
		mount := ocispec.Mount{
			Destination: m.Destination,
			Type:        m.Type,
			Source:      m.Source,
			Options:     m.Options,
		}
		if m.ReadOnly {
			if mount.Options == nil {
				mount.Options = []string{"ro"}
			} else {
				mount.Options = append(mount.Options, "ro")
			}
		}
		mounts = append(mounts, mount)
	}

	return mounts
}
