package driver

import (
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
)

// Config contains configuration information for the plugin
type Config struct {
	FirecrackerBinPath  string `codec:"firecracker_bin_path"`
	FirecrackerDataPath string `codec:"firecracker_data_path"`
	CNIConfigPath       string `codec:"cni_config_path"`
	CNIBinPath          string `codec:"cni_bin_path"`
	CNIDataPath         string `codec:"cni_data_path"`
}

// TaskConfig contains configuration information for a task that runs with
// this plugin
type TaskConfig struct {
	Kernel     string `codec:"kernel"`
	KernelArgs string `codec:"kernel_args"`
	Disk       []Disk `codec:"disk"`
	Network    string `codec:"network"`
	Metadata   string `codec:"metadata"`
}

type Disk struct {
	RootDevice bool   `codec:"root_device"`
	ReadOnly   bool   `codec:"readonly"`
	Path       string `codec:"path"`
}

var configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
	"firecracker_bin_path": hclspec.NewDefault(
		hclspec.NewAttr("firecracker_bin_path", "string", false),
		hclspec.NewLiteral(`"/usr/bin/firecracker"`),
	),
	"firecracker_data_path": hclspec.NewDefault(
		hclspec.NewAttr("firecracker_data_path", "string", false),
		hclspec.NewLiteral(`"/run/firecracker"`),
	),
	"cni_config_path": hclspec.NewDefault(
		hclspec.NewAttr("cni_config_path", "string", false),
		hclspec.NewLiteral(`"/etc/cni/conf.d"`),
	),
	"cni_bin_path": hclspec.NewDefault(
		hclspec.NewAttr("cni_bin_path", "string", false),
		hclspec.NewLiteral(`"/opt/cni/bin"`),
	),
	"cni_data_path": hclspec.NewDefault(
		hclspec.NewAttr("cni_data_path", "string", false),
		hclspec.NewLiteral(`"/var/lib/cni/networks"`),
	),
})

var taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
	"kernel": hclspec.NewAttr("kernel", "string", true),
	"kernel_args": hclspec.NewDefault(
		hclspec.NewAttr("kernel_args", "string", false),
		hclspec.NewLiteral(`"console=ttyS0 reboot=k panic=1 pci=off"`),
	),
	"disk": hclspec.NewBlockList("disk", hclspec.NewObject(map[string]*hclspec.Spec{
		"root_device": hclspec.NewDefault(
			hclspec.NewAttr("root_device", "bool", false),
			hclspec.NewLiteral(`false`),
		),
		"readonly": hclspec.NewDefault(
			hclspec.NewAttr("readonly", "bool", false),
			hclspec.NewLiteral(`false`),
		),
		"path": hclspec.NewAttr("path", "string", true),
	})),
	"network": hclspec.NewDefault(
		hclspec.NewAttr("network", "string", false),
		hclspec.NewLiteral(`"firecracker"`),
	),
	"metadata": hclspec.NewDefault(
		hclspec.NewAttr("metadata", "string", false),
		hclspec.NewLiteral(`"{}"`),
	),
})
