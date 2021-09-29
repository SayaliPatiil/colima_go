package docker

import (
	"github.com/abiosoft/colima/cli"
	"github.com/abiosoft/colima/config"
	"github.com/abiosoft/colima/environment"
	"os"
)

// Name is container runtime name.
const Name = "docker"

var _ environment.Container = (*dockerRuntime)(nil)

func init() {
	environment.RegisterContainer(Name, newRuntime)
}

type dockerRuntime struct {
	host  environment.HostActions
	guest environment.GuestActions
	cli.CommandChain
	launchd launchAgent
}

// NewContainer creates a new docker runtime.
func newRuntime(host environment.HostActions, guest environment.GuestActions) environment.Container {
	launchdPkg := "com.abiosoft." + config.AppName()

	return &dockerRuntime{
		host:         host,
		guest:        guest,
		CommandChain: cli.New(Name),
		launchd:      launchAgent(launchdPkg),
	}
}

func (d dockerRuntime) Name() string {
	return Name
}

func (d dockerRuntime) isInstalled() bool {
	err := d.guest.Run("command", "-v", "docker")
	return err == nil
}

func (d dockerRuntime) isUserPermissionFixed() bool {
	err := d.guest.Run("sh", "-c", `getent group docker | grep "\b${USER}\b"`)
	return err == nil
}

func (d dockerRuntime) Provision() error {
	a := d.Init()
	a.Stage("provisioning")

	// check installation
	if !d.isInstalled() {
		a.Stage("setting up socket")
		a.Add(d.setupSocketSymlink)

		a.Stage("provisioning in VM")
		a.Add(d.setupInVM)
	}

	// check user permission
	if !d.isUserPermissionFixed() {
		a.Add(d.fixUserPermission)

		a.Stage("restarting VM to complete setup")
		a.Add(d.guest.Restart)
	}

	// socket file/launchd
	a.Add(func() error {
		user, err := d.guest.User()
		if err != nil {
			return err
		}
		return createSocketForwardingScript(user)
	})
	a.Add(func() error { return createLaunchdScript(d.launchd) })

	return a.Exec()
}

func (d dockerRuntime) Start() error {
	a := d.Init()
	a.Stage("starting")

	a.Add(func() error {
		return d.guest.Run("sudo", "service", "docker", "start")
	})
	a.Add(func() error {
		return d.host.Run("launchctl", "load", d.launchd.File())
	})

	return a.Exec()
}

func (d dockerRuntime) Stop() error {
	a := d.Init()
	a.Stage("stopping")

	a.Add(func() error {
		if d.guest.Run("service", "docker", "status") != nil {
			return nil
		}
		return d.guest.Run("sudo", "service", "docker", "stop")
	})
	a.Add(func() error {
		return d.host.Run("launchctl", "unload", d.launchd.File())
	})

	return a.Exec()
}

func (d dockerRuntime) Teardown() error {
	a := d.Init()
	a.Stage("deleting")

	// no need to uninstall as the VM teardown will remove all components
	// only host configurations should be removed
	if stat, err := os.Stat(d.launchd.File()); err == nil && !stat.IsDir() {
		a.Add(func() error {
			return d.host.Run("launchctl", "unload", d.launchd.File())
		})
		a.Add(func() error {
			return d.host.Run("rm", "-rf", d.launchd.File())
		})
	}

	return a.Exec()
}

func (d dockerRuntime) Dependencies() []string {
	return []string{"docker"}
}

func (d dockerRuntime) Version() string {
	version, _ := d.host.RunOutput("docker", "version", "--format", `client: v{{.Client.Version}}{{printf "\n"}}server: v{{.Server.Version}}`)
	return version
}
