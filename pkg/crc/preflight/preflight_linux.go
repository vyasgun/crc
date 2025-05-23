package preflight

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/crc-org/crc/v2/pkg/crc/constants"
	crcErrors "github.com/crc-org/crc/v2/pkg/crc/errors"
	"github.com/crc-org/crc/v2/pkg/crc/logging"
	"github.com/crc-org/crc/v2/pkg/crc/network"
	crcpreset "github.com/crc-org/crc/v2/pkg/crc/preset"
	crcos "github.com/crc-org/crc/v2/pkg/os"
	"github.com/crc-org/crc/v2/pkg/os/linux"

	"golang.org/x/sys/unix"
)

func libvirtPreflightChecks(distro *linux.OsRelease) []Check {
	checks := []Check{
		{
			configKeySuffix:  "check-virt-enabled",
			checkDescription: "Checking if Virtualization is enabled",
			check:            checkVirtualizationEnabled,
			fixDescription:   "Setting up virtualization",
			fix:              fixVirtualizationEnabled,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-kvm-enabled",
			checkDescription: "Checking if KVM is enabled",
			check:            checkKvmEnabled,
			fixDescription:   "Setting up KVM",
			fix:              fixKvmEnabled,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-libvirt-installed",
			checkDescription: "Checking if libvirt is installed",
			check:            checkLibvirtInstalled,
			fixDescription:   "Installing libvirt service and dependencies",
			fix:              fixLibvirtInstalled(distro),

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-user-in-libvirt-group",
			checkDescription: "Checking if user is part of libvirt group",
			check:            checkUserPartOfLibvirtGroup,
			fixDescription:   "Adding user to libvirt group",
			fix:              fixUserPartOfLibvirtGroup,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-libvirt-group-active",
			checkDescription: "Checking if active user/process is currently part of the libvirt group",
			check:            checkCurrentGroups(distro),
			fixDescription:   "You need to logout, re-login, and run crc setup again before the user is effectively a member of the 'libvirt' group.",
			flags:            NoFix,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-libvirt-running",
			checkDescription: "Checking if libvirt daemon is running",
			check:            checkLibvirtServiceRunning,
			fixDescription:   "Starting libvirt service",
			fix:              fixLibvirtServiceRunning,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-libvirt-version",
			checkDescription: "Checking if a supported libvirt version is installed",
			check:            checkLibvirtVersion,
			fixDescription:   fmt.Sprintf("libvirt v%s or newer is required and must be updated manually", minSupportedLibvirtVersion),
			flags:            NoFix,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:  "check-libvirt-driver",
			checkDescription: "Checking if crc-driver-libvirt is installed",
			check:            checkMachineDriverLibvirtInstalled,
			fixDescription:   "Installing crc-driver-libvirt",
			fix:              fixMachineDriverLibvirtInstalled,

			labels: labels{Os: Linux},
		},
		{
			cleanupDescription: "Removing crc libvirt storage pool",
			cleanup:            removeLibvirtStoragePool,
			flags:              CleanUpOnly,

			labels: labels{Os: Linux},
		},
		{
			cleanupDescription: "Removing crc's virtual machine",
			cleanup:            removeCrcVM,
			flags:              CleanUpOnly,

			labels: labels{Os: Linux},
		},
		{
			configKeySuffix:    "check-daemon-systemd-unit",
			checkDescription:   "Checking crc daemon systemd service",
			check:              checkDaemonSystemdService,
			fixDescription:     "Setting up crc daemon systemd service",
			fix:                fixDaemonSystemdService,
			cleanupDescription: "Removing crc daemon systemd service",
			cleanup:            removeDaemonSystemdService,
			flags:              SetupOnly,

			labels: labels{Os: Linux, SystemdUser: Supported},
		},
		{
			configKeySuffix:    "check-daemon-systemd-sockets",
			checkDescription:   "Checking crc daemon systemd socket units",
			check:              checkDaemonSystemdSockets,
			fixDescription:     "Setting up crc daemon systemd socket units",
			fix:                fixDaemonSystemdSockets,
			cleanupDescription: "Removing crc daemon systemd socket units",
			cleanup:            removeDaemonSystemdSockets,

			labels: labels{Os: Linux, SystemdUser: Supported},
		},
		{
			checkDescription: "Checking if crc daemon will be autostarted",
			check:            warnNoDaemonAutostart,

			labels: labels{Os: Linux, NetworkMode: User, SystemdUser: Unsupported},
		},
	}
	return checks
}

var libvirtNetworkPreflightChecks = []Check{
	{
		configKeySuffix:    "check-crc-network",
		checkDescription:   "Checking if libvirt 'crc' network is available",
		check:              checkLibvirtCrcNetworkAvailable,
		fixDescription:     "Setting up libvirt 'crc' network",
		fix:                fixLibvirtCrcNetworkAvailable,
		cleanupDescription: "Removing 'crc' network from libvirt",
		cleanup:            removeLibvirtCrcNetwork,

		labels: labels{Os: Linux, NetworkMode: System},
	},
	{
		configKeySuffix:  "check-crc-network-active",
		checkDescription: "Checking if libvirt 'crc' network is active",
		check:            checkLibvirtCrcNetworkActive,
		fixDescription:   "Starting libvirt 'crc' network",
		fix:              fixLibvirtCrcNetworkActive,

		labels: labels{Os: Linux, NetworkMode: System},
	},
}

var vsockPreflightCheck = Check{
	configKeySuffix:    "check-vsock",
	checkDescription:   "Checking if vsock is correctly configured",
	check:              checkVsock,
	fixDescription:     "Setting up vsock support",
	fix:                fixVsock,
	cleanupDescription: "Removing vsock configuration",
	cleanup:            removeVsockCrcSettings,

	labels: labels{Os: Linux, NetworkMode: User},
}

var wsl2PreflightCheck = Check{
	configKeySuffix:  "check-wsl2",
	checkDescription: "Checking if running inside WSL2",
	check:            checkRunningInsideWSL2,
	fixDescription:   "CRC is unsupported using WSL2",
	flags:            NoFix,

	labels: labels{Os: Linux},
}

const (
	vsockUdevSystemRulesPath     = "/usr/lib/udev/rules.d/99-crc-vsock.rules"
	vsockUdevLocalAdminRulesPath = "/etc/udev/rules.d/99-crc-vsock.rules"
	vsockModuleAutoLoadConfPath  = "/etc/modules-load.d/vhost_vsock.conf"
)

func checkVsock() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	getcap, _, err := crcos.RunWithDefaultLocale("getcap", executable)
	if err != nil {
		return err
	}
	if !strings.Contains(getcap, "cap_net_bind_service+eip") &&
		!strings.Contains(getcap, "cap_net_bind_service=eip") {
		return fmt.Errorf("capabilities are not correct for %s", executable)
	}

	// This test is needed in order to trigger the move of the udev rule to its new location.
	// The old location was used in the 1.21 release.
	if !crcos.FileExists(vsockUdevLocalAdminRulesPath) {
		return errors.New("vsock udev rule does not exist")
	}

	err = unix.Access("/dev/vsock", unix.R_OK|unix.W_OK)
	if err != nil {
		return errors.New("/dev/vsock is not readable by the current user")
	}
	return nil
}

func fixVsock() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	_, _, err = crcos.RunPrivileged(fmt.Sprintf("Setting CAP_NET_BIND_SERVICE capability for %s executable", executable), "setcap", "cap_net_bind_service=+eip", executable)
	if err != nil {
		return err
	}

	// Remove udev rule which was used in crc 1.21 - it's been moved to a new location
	err = crcos.RemoveFileAsRoot(
		fmt.Sprintf("Removing udev rule in %s", vsockUdevSystemRulesPath),
		vsockUdevSystemRulesPath,
	)
	if err != nil {
		return err
	}
	udevRule := `KERNEL=="vsock", MODE="0660", OWNER="root", GROUP="libvirt"`
	if crcos.FileContentMatches(vsockUdevLocalAdminRulesPath, []byte(udevRule)) != nil {
		err = crcos.WriteToFileAsRoot("Creating udev rule for /dev/vsock", udevRule, vsockUdevLocalAdminRulesPath, 0644)
		if err != nil {
			return err
		}
		_, _, err = crcos.RunPrivileged("Reloading udev rules database", "udevadm", "control", "--reload")
		if err != nil {
			return err
		}
	}
	if crcos.FileExists("/dev/vsock") && unix.Access("/dev/vsock", unix.R_OK|unix.W_OK) != nil {
		_, _, err = crcos.RunPrivileged("Applying udev rule to /dev/vsock", "udevadm", "trigger", "/dev/vsock")
		if err != nil {
			return err
		}
	} else {
		_, _, err = crcos.RunPrivileged("Loading vhost_vsock kernel module", "modprobe", "vhost_vsock")
		if err != nil {
			return err
		}
	}

	if crcos.FileContentMatches(vsockModuleAutoLoadConfPath, []byte("vhost_vsock")) != nil {
		err = crcos.WriteToFileAsRoot(fmt.Sprintf("Creating file %s", vsockModuleAutoLoadConfPath), "vhost_vsock", vsockModuleAutoLoadConfPath, 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

func removeVsockCrcSettings() error {
	var mErr crcErrors.MultiError
	err := crcos.RemoveFileAsRoot(fmt.Sprintf("Removing udev rule in %s", vsockUdevSystemRulesPath), vsockUdevSystemRulesPath)
	if err != nil {
		mErr.Collect(err)
	}
	err = crcos.RemoveFileAsRoot(fmt.Sprintf("Removing udev rule in %s", vsockUdevLocalAdminRulesPath), vsockUdevLocalAdminRulesPath)
	if err != nil {
		mErr.Collect(err)
	}
	err = crcos.RemoveFileAsRoot(fmt.Sprintf("Removing vsock module autoload file %s", vsockModuleAutoLoadConfPath), vsockModuleAutoLoadConfPath)
	if err != nil {
		mErr.Collect(err)
	}
	if len(mErr.Errors) == 0 {
		return nil
	}
	return mErr
}

const (
	Distro LabelName = iota + lastLabelName
	DNS
	SystemdUser
)

const (
	// distro
	UbuntuLike LabelValue = iota + lastLabelValue
	Other

	// dns
	Dnsmasq
	SystemdResolved

	// systemd user session
	Supported
	Unsupported
)

func (filter preflightFilter) SetSystemdResolved(usingSystemdResolved bool) {
	if usingSystemdResolved {
		filter[DNS] = SystemdResolved
	} else {
		filter[DNS] = Dnsmasq
	}
}

func (filter preflightFilter) SetDistro(distro *linux.OsRelease) {
	if distroIsLike(distro, linux.Ubuntu) {
		filter[Distro] = UbuntuLike
	} else {
		filter[Distro] = Other
	}
}

func (filter preflightFilter) SetSystemdUser(distro *linux.OsRelease) {
	switch {
	case distroIsLike(distro, linux.RHEL) && (distro.VersionID == "7" || strings.HasPrefix(distro.VersionID, "7.")):
		filter[SystemdUser] = Unsupported
	default:
		filter[SystemdUser] = Supported
	}
}

// We want all preflight checks
// - matching the current distro
// - matching the networking daemon in use (NetworkManager or systemd-resolved) regardless of user/system networking
// - and we also want the user networking checks
func getAllPreflightChecks() []Check {
	usingSystemdResolved := checkSystemdResolvedIsRunning()
	filter := newFilter()
	filter.SetSystemdResolved(usingSystemdResolved == nil)
	filter.SetDistro(distro())
	filter.SetSystemdUser(distro())

	return filter.Apply(getChecks(distro(), constants.GetDefaultBundlePath(crcpreset.OpenShift), crcpreset.OpenShift, false))
}

func getPreflightChecks(_ bool, networkMode network.Mode, bundlePath string, preset crcpreset.Preset, enableBundleQuayFallback bool) []Check {
	usingSystemdResolved := checkSystemdResolvedIsRunning()

	return getPreflightChecksForDistro(distro(), networkMode, usingSystemdResolved == nil, bundlePath, preset, enableBundleQuayFallback)
}

func getPreflightChecksForDistro(distro *linux.OsRelease, networkMode network.Mode, usingSystemdResolved bool, bundlePath string, preset crcpreset.Preset, enableBundleQuayFallback bool) []Check {
	filter := newFilter()
	filter.SetDistro(distro)
	filter.SetSystemdUser(distro)
	filter.SetNetworkMode(networkMode)
	filter.SetSystemdResolved(usingSystemdResolved)

	return filter.Apply(getChecks(distro, bundlePath, preset, enableBundleQuayFallback))
}

func getChecks(distro *linux.OsRelease, bundlePath string, preset crcpreset.Preset, enableBundleQuayFallback bool) []Check {
	var checks []Check
	checks = append(checks, nonWinPreflightChecks...)
	checks = append(checks, wsl2PreflightCheck)
	checks = append(checks, genericPreflightChecks(preset)...)
	checks = append(checks, memoryCheck(preset))
	checks = append(checks, removePodmanFromOcBinDirCheck())
	checks = append(checks, genericCleanupChecks...)
	checks = append(checks, libvirtPreflightChecks(distro)...)
	checks = append(checks, ubuntuPreflightChecks...)
	checks = append(checks, nmPreflightChecks...)
	checks = append(checks, systemdResolvedPreflightChecks...)
	checks = append(checks, dnsmasqPreflightChecks...)
	checks = append(checks, libvirtNetworkPreflightChecks...)
	checks = append(checks, vsockPreflightCheck)
	checks = append(checks, bundleCheck(bundlePath, preset, enableBundleQuayFallback))

	return checks
}

func distroIsLike(osRelease *linux.OsRelease, osType linux.OsType) bool {
	if osRelease == nil {
		return false
	}
	if osRelease.ID == osType {
		return true
	}

	for _, id := range osRelease.GetIDLike() {
		if id == osType {
			return true
		}
	}

	return false
}

func distro() *linux.OsRelease {
	distro, err := linux.GetOsRelease()
	if err != nil {
		logging.Errorf("cannot get distribution name: %v", err)
		return &linux.OsRelease{
			ID: "unknown",
		}
	}
	return distro
}
