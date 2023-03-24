package driver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/hashicorp/consul-template/signals"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/lib/fifo"
	"github.com/hashicorp/nomad/client/stats"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	pluginName        = "firecracker"
	pluginVersion     = "v0.1.0"
	fingerprintPeriod = 30 * time.Second
	taskHandleVersion = 1
)

var (
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     pluginVersion,
		Name:              pluginName,
	}

	capabilities = &drivers.Capabilities{
		SendSignals:         true,
		Exec:                false,
		MustInitiateNetwork: false,
		NetIsolationModes: []drivers.NetIsolationMode{
			drivers.NetIsolationModeGroup,
		},
	}
)

type TaskState struct {
	ReattachConfig *structs.ReattachConfig
	TaskConfig     *drivers.TaskConfig
	StartedAt      time.Time
}

type FirecrackerDriverPlugin struct {
	eventer        *eventer.Eventer
	config         *Config
	nomadConfig    *base.ClientDriverConfig
	tasks          *taskStore
	ctx            context.Context
	signalShutdown context.CancelFunc
	logger         hclog.Logger
}

// NewPlugin returns a new driver plugin
func NewPlugin(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)

	return &FirecrackerDriverPlugin{
		eventer:        eventer.NewEventer(ctx, logger),
		config:         &Config{},
		tasks:          newTaskStore(),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

// PluginInfo returns information describing the plugin.
func (d *FirecrackerDriverPlugin) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

// ConfigSchema returns the plugin configuration schema.
func (d *FirecrackerDriverPlugin) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

// SetConfig is called by the client to pass the configuration for the plugin.
func (d *FirecrackerDriverPlugin) SetConfig(cfg *base.Config) error {
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	d.config = &config

	// TODO: parse and validated any configuration value if necessary.

	firecracker := d.config.FirecrackerBinPath
	_, err := os.Stat(firecracker)
	if err != nil {
		return fmt.Errorf("firecracker binary not found at %s", d.config.FirecrackerBinPath)
	}

	// Save the Nomad agent configuration
	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}

	// Here you can use the config values to initialize any resources that are
	// shared by all tasks that use this driver, such as a daemon process.

	/* TODO:
	- if not exist -> create cni network conflist at cni_config_path
	- if not exist -> create firecracker run directory to store sockets, fifo, etc. at firecracker_data_path
	*/

	return nil
}

// TaskConfigSchema returns the HCL schema for the configuration of a task.
func (d *FirecrackerDriverPlugin) TaskConfigSchema() (*hclspec.Spec, error) {
	return taskConfigSpec, nil
}

// Capabilities returns the features supported by the driver.
func (d *FirecrackerDriverPlugin) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

// Fingerprint returns a channel that will be used to send health information
// and other driver specific node attributes.
func (d *FirecrackerDriverPlugin) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

// handleFingerprint manages the channel and the flow of fingerprint data.
func (d *FirecrackerDriverPlugin) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	defer close(ch)

	// Nomad expects the initial fingerprint to be sent immediately
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// after the initial fingerprint we can set the proper fingerprint
			// period
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

// buildFingerprint returns the driver's fingerprint data
func (d *FirecrackerDriverPlugin) buildFingerprint() *drivers.Fingerprint {
	fp := &drivers.Fingerprint{
		Attributes:        map[string]*structs.Attribute{},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	// check if we have firecracker installed
	// check firecracker version
	firecracker := d.config.FirecrackerBinPath
	if _, err := os.Stat(firecracker); err != nil {
		return &drivers.Fingerprint{
			Health:            drivers.HealthStateUndetected,
			HealthDescription: fmt.Sprintf("firecracker binary not found at %s", d.config.FirecrackerBinPath),
		}
	}

	cmd := exec.Command(firecracker, "--version")
	if out, err := cmd.Output(); err != nil {
		d.logger.Warn("failed to get firecracker version: %v", err)
	} else {
		re := regexp.MustCompile(`[0-9]\\.[0-9]\\.[0-9]`)
		version := re.FindString(string(out))

		fp.Attributes["driver.firecracker.firecracker_version"] = structs.NewStringAttribute(version)
		fp.Attributes["driver.firecracker.firecracker_binary"] = structs.NewStringAttribute(firecracker)
	}

	// check if we have cni installed
	// check cni version

	return fp
}

// StartTask returns a task handle and a driver network if necessary.
func (d *FirecrackerDriverPlugin) StartTask(taskConfig *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	if _, ok := d.tasks.Get(taskConfig.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", taskConfig.ID)
	}

	var driverConfig TaskConfig
	if err := taskConfig.DecodeDriverConfig(&driverConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode driver config: %v", err)
	}

	d.logger.Info("starting task", "driver_cfg", hclog.Fmt("%+v", driverConfig))
	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = taskConfig

	// Firecracker logic start

	// To expose a local tty that we can use with screen: e.g. sudo screen /dev/pts/5
	// _, ftty, err := console.NewPty()
	// if err != nil {
	// 	return nil, nil, fmt.Errorf("could not create serial console  %v+", err)
	// }

	// d.logger.Debug("serial console available", "pty", ftty)

	// Create run dir for allocation data
	runPath := filepath.Join(d.config.FirecrackerDataPath, taskConfig.AllocID)
	err := os.MkdirAll(runPath, 0700)
	if err != nil {
		return nil, nil, fmt.Errorf("could not create %s: %v+", runPath, err)
	}

	drives := []models.Drive{}
	for index, disk := range driverConfig.Disk {
		drives = append(drives, models.Drive{
			DriveID:      firecracker.String(fmt.Sprintf("disk_%d", index)),
			IsRootDevice: firecracker.Bool(disk.RootDevice),
			IsReadOnly:   firecracker.Bool(disk.ReadOnly),
			PathOnHost:   firecracker.String(disk.Path),
		})
	}

	fw, err := fifo.OpenWriter(taskConfig.StdoutPath)
	if err != nil {
		return nil, nil, fmt.Errorf("could not open fifo writer  %v+", err)
	}

	// d.logger.Debug("DEBUG", "config", taskConfig)

	allocIP, err := d.GetCNIIP()
	if err != nil {
		return nil, nil, fmt.Errorf("could not get IP address  %v+", err)
	}

	gatewayIP := d.GetGatewayForIP(allocIP)

	macAddress, err := d.GenerateMac()
	if err != nil {
		return nil, nil, fmt.Errorf("could not generate mac address  %v+", err)
	}

	taskConfig.NetworkIsolation.HostsConfig = &drivers.HostsConfig{
		Address:  allocIP.String(),
		Hostname: "one",
	}

	cfg := firecracker.Config{
		VMID: taskConfig.AllocID,
		// JailerCfg: &sdk.JailerConfig{
		// 	GID:            firecracker.Int(0),
		// 	UID:            firecracker.Int(0),
		// 	NumaNode:       firecracker.Int(0),
		// 	JailerBinary:   "/usr/bin/jailer",
		// 	ExecFile:       "/usr/bin/firecracker",
		// 	ChrootBaseDir:  "/srv/jailer",
		// 	ChrootStrategy: firecracker.NewNaiveChrootStrategy(filepath.Join(machinePath, "vmlinux")),
		// 	// 	Stdout:         os.Stdout,
		// 	// 	Stderr:         os.Stderr,
		// 	// 	Stdin:          os.Stdin,
		// 	ID: taskConfig.AllocID,
		// 	// 	Daemonize:      true,
		// },
		NetNS:           taskConfig.NetworkIsolation.Path,
		SocketPath:      filepath.Join(runPath, "socket"),
		LogFifo:         filepath.Join(runPath, "fifo"),
		FifoLogWriter:   fw,
		LogLevel:        "Debug",
		MetricsPath:     filepath.Join(taskConfig.AllocDir, "metrics"),
		KernelImagePath: driverConfig.Kernel,
		KernelArgs:      driverConfig.KernelArgs,
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(d.CalculateVcpuCount(taskConfig.Resources.NomadResources.Cpu.CpuShares)),
			MemSizeMib: firecracker.Int64(taskConfig.Resources.NomadResources.Memory.MemoryMB),
			Smt:        firecracker.Bool(false),
		},
		Drives: drives,
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					HostDevName: "tap0",
					MacAddress:  macAddress.String(),
					IPConfiguration: &firecracker.IPConfiguration{
						IfName: "eth0",
						IPAddr: net.IPNet{
							IP:   allocIP,
							Mask: allocIP.DefaultMask(),
						},
						Gateway: gatewayIP,
					},
				},
				AllowMMDS: true,
			},
			// {
			// 	CNIConfiguration: &firecracker.CNIConfiguration{
			// 		NetworkName: "firecracker",
			// 		IfName:      "tap0",
			// 		ConfDir:     d.config.CNIConfigPath,
			// 		BinPath:     []string{d.config.CNIBinPath},
			// 		VMIfName:    "eth0",
			// 	},
			// 	AllowMMDS: true,
			// },
			// {
		},
		VsockDevices: []firecracker.VsockDevice{},
	}

	err = cfg.Validate()
	if err != nil {
		return nil, nil, fmt.Errorf("vm configuration is invalid  %v+", err)
	}

	d.logger.Debug("cfg", cfg)

	// cmd := firecracker.VMCommandBuilder{}.
	// 	WithBin("firecracker").
	// 	WithSocketPath(cfg.SocketPath).
	// // WithStdin(os.Stdin).
	// // WithStdout(os.Stdout).
	// // WithStderr(os.Stderr).
	// 	Build(d.ctx)

	machineOpts := []firecracker.Opt{}
	// machineOpts = append(
	// 	machineOpts,

	// 	firecracker.WithProcessRunner(cmd),
	// )

	machine, err := firecracker.NewMachine(d.ctx, cfg, machineOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating machine: %v+", err)
	}

	if err := machine.Start(d.ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to start machine: %v+", err)
	}

	var metadata interface{}
	if err := json.Unmarshal([]byte(driverConfig.Metadata), &metadata); err != nil {
		return nil, nil, fmt.Errorf("could not unmarshall metadata: %v+", err)
	}

	if err := machine.SetMetadata(d.ctx, metadata); err != nil {
		return nil, nil, fmt.Errorf("an error occurred while setting Firecracker VM metadata: %v+", err)
	}

	// Firecracker logic end

	h := &taskHandle{
		ctx:           d.ctx,
		machine:       machine,
		taskConfig:    taskConfig,
		taskState:     drivers.TaskStateRunning,
		startedAt:     time.Now().Round(time.Millisecond),
		logger:        d.logger,
		cpuStatsSys:   stats.NewCpuStats(),
		cpuStatsUser:  stats.NewCpuStats(),
		cpuStatsTotal: stats.NewCpuStats(),
	}

	driverState := TaskState{
		TaskConfig: taskConfig,
		StartedAt:  h.startedAt,
	}

	if err := handle.SetDriverState(&driverState); err != nil {
		return nil, nil, fmt.Errorf("failed to set driver state: %v", err)
	}

	d.tasks.Set(taskConfig.ID, h)
	go h.run()
	return handle, nil, nil
}

func (d *FirecrackerDriverPlugin) Shutdown(ctx context.Context) error {
	d.signalShutdown()
	return nil
}

// RecoverTask recreates the in-memory state of a task from a TaskHandle.
func (d *FirecrackerDriverPlugin) RecoverTask(handle *drivers.TaskHandle) error {
	if handle == nil {
		return errors.New("error: handle cannot be nil")
	}

	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		return nil
	}

	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		return fmt.Errorf("failed to decode task state from handle: %v", err)
	}

	var driverConfig TaskConfig
	if err := taskState.TaskConfig.DecodeDriverConfig(&driverConfig); err != nil {
		return fmt.Errorf("failed to decode driver config: %v", err)
	}

	h := &taskHandle{
		taskConfig: taskState.TaskConfig,
		taskState:  drivers.TaskStateRunning,
		startedAt:  taskState.StartedAt,
		exitResult: &drivers.ExitResult{},
	}

	d.tasks.Set(taskState.TaskConfig.ID, h)

	go h.run()
	return nil
}

// WaitTask returns a channel used to notify Nomad when a task exits.
func (d *FirecrackerDriverPlugin) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	ch := make(chan *drivers.ExitResult)
	go d.handleWait(ctx, handle, ch)
	return ch, nil
}

func (d *FirecrackerDriverPlugin) handleWait(ctx context.Context, handle *taskHandle, ch chan *drivers.ExitResult) {
	defer close(ch)

	// Going with simplest approach of polling for handler to mark exit.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			s := handle.status()
			if s.State == drivers.TaskStateExited {
				ch <- handle.exitResult
			}
		}
	}
}

// StopTask stops a running task with the given signal and within the timeout window.
func (d *FirecrackerDriverPlugin) StopTask(taskID string, timeout time.Duration, signal string) error {
	d.logger.Info("!!!!!!!!!! stopping task", "taskID", taskID)

	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if err := handle.shutdown(timeout); err != nil {
		return fmt.Errorf("executor Shutdown failed: %v", err)
	}

	return nil
}

// DestroyTask cleans up and removes a task that has terminated.
func (d *FirecrackerDriverPlugin) DestroyTask(taskID string, force bool) error {
	d.logger.Info("!!!!!!!!!! destroy task", "taskID", taskID)

	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if handle.IsRunning() && !force {
		return errors.New("cannot destroy running task")
	}

	// Destroying a task includes removing any resources used by task and any
	// local references in the plugin. If force is set to true the task should
	// be destroyed even if it's currently running.
	//
	// In the example below we use the executor to force shutdown the task
	// (timeout equals 0).
	if handle.IsRunning() {
		// grace period is chosen arbitrary here
		if err := handle.shutdown(1 * time.Minute); err != nil {
			handle.logger.Error("failed to destroy executor", "err", err)
		}

		// Wait for stats handler to report that the task has exited
		for i := 0; i < 10; i++ {
			if !handle.IsRunning() {
				break
			}
			time.Sleep(time.Millisecond * 250)
		}
	}

	d.logger.Info("!!!!!!!!!! destroyed task", "taskID", taskID)

	d.tasks.Delete(taskID)
	return nil
}

// InspectTask returns detailed status information for the referenced taskID.
func (d *FirecrackerDriverPlugin) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.status(), nil
}

// TaskStats returns a channel which the driver should send stats to at the given interval.
func (d *FirecrackerDriverPlugin) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	statsChannel := make(chan *drivers.TaskResourceUsage)
	go handle.stats(ctx, statsChannel, interval)

	return statsChannel, nil
}

// TaskEvents returns a channel that the plugin can use to emit task related events.
func (d *FirecrackerDriverPlugin) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return d.eventer.TaskEvents(ctx)
}

// SignalTask forwards a signal to a task.
// This is an optional capability.
func (d *FirecrackerDriverPlugin) SignalTask(taskID string, signal string) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	sig := os.Interrupt
	if s, ok := signals.SignalLookup[signal]; ok {
		sig = s
	} else {
		d.logger.Warn("unknown signal to send to task, using SIGINT instead", "signal", signal, "task_id", handle.taskConfig.ID)

	}
	return handle.signal(sig)
}

func (d *FirecrackerDriverPlugin) CalculateVcpuCount(shares int64) int64 {
	cores := math.Floor(float64(shares) / 1000)
	if cores < 1 {
		return 1
	} else {
		return int64(cores)
	}
}

// ExecTask returns the result of executing the given command inside a task.
// This is an optional capability.
func (d *FirecrackerDriverPlugin) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	return nil, errors.New("this driver does not support exec")
}

// Get the last IP reserved by CNI.
// HACK: Currently we get the IP that was allocated to the allocation from CNI helper file.
// TODO: Replace with cni.Result when available to the driver: https://github.com/hashicorp/nomad/issues/16624
func (d *FirecrackerDriverPlugin) GetCNIIP() (net.IP, error) {
	lastReservedIp, err := ioutil.ReadFile(filepath.Join(d.config.CNIDataPath, "/firecracker/last_reserved_ip.0"))
	if err != nil {
		return nil, fmt.Errorf("could not read last_reserved_ip.0  %v+", err)
	}

	parsedIP := net.ParseIP(string(lastReservedIp)).To4()
	if parsedIP == nil {
		if err != nil {
			return nil, fmt.Errorf("could not parse last_reserved_ip  %v+", err)
		}
	}

	return parsedIP, nil
}

// Get the gateway IP for a given IP.
// Assume gateway is the first IP in the range.
// TODO: replace with cni.Result when available to the driver: https://github.com/hashicorp/nomad/issues/16624
func (d *FirecrackerDriverPlugin) GetGatewayForIP(ip net.IP) net.IP {
	return net.IPv4(ip[0], ip[1], ip[2], 1)
}

// Generate random MAC address.
func (d *FirecrackerDriverPlugin) GenerateMac() (net.HardwareAddr, error) {
	buf := make([]byte, 6)
	var mac net.HardwareAddr

	_, err := rand.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("could not generate mac address  %v+", err)
	}

	buf[0] = (buf[0] | 2) & 0xfe // Set local bit, ensure unicast address

	mac = append(mac, buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])

	return mac, nil
}
