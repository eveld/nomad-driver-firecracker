package driver

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/stats"
	"github.com/hashicorp/nomad/plugins/drivers"
)

// taskHandle should store all relevant runtime information
// such as process ID if this is a local task or other meta
// data if this driver deals with external APIs
type taskHandle struct {
	// stateLock syncs access to all fields below
	stateLock sync.RWMutex

	logger      hclog.Logger
	taskConfig  *drivers.TaskConfig
	taskState   drivers.TaskState
	startedAt   time.Time
	completedAt time.Time
	exitResult  *drivers.ExitResult

	machine *firecracker.Machine
	ctx     context.Context

	cpuStatsSys   *stats.CpuStats
	cpuStatsUser  *stats.CpuStats
	cpuStatsTotal *stats.CpuStats
}

func (h *taskHandle) status() *drivers.TaskStatus {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()

	return &drivers.TaskStatus{
		ID:               h.taskConfig.ID,
		Name:             h.taskConfig.Name,
		State:            h.taskState,
		StartedAt:        h.startedAt,
		CompletedAt:      h.completedAt,
		ExitResult:       h.exitResult,
		DriverAttributes: map[string]string{},
	}
}

func (h *taskHandle) IsRunning() bool {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()

	return h.taskState == drivers.TaskStateRunning
}

func (h *taskHandle) run() {
	h.stateLock.Lock()
	if h.exitResult == nil {
		h.exitResult = &drivers.ExitResult{}
	}
	h.stateLock.Unlock()

	h.machine.Wait(h.ctx)

	h.stateLock.Lock()
	defer h.stateLock.Unlock()

	if err := h.machine.StopVMM(); err != nil {
		if err != nil {
			h.logger.Error("handle.run: something went wrong while stopping machine", "error", err)
			h.exitResult.Err = err
			h.taskState = drivers.TaskStateUnknown
			h.completedAt = time.Now()
			return
		}
	}

	h.taskState = drivers.TaskStateExited
	h.exitResult.ExitCode = 0
	h.exitResult.Signal = 0
	h.completedAt = time.Now()
}

func (h *taskHandle) shutdown(timeout time.Duration) error {
	time.Sleep(timeout)

	err := h.machine.Shutdown(h.ctx)
	if err != nil {
		h.logger.Error("handle.shutdown: something went wrong while stopping machine", "error", err)
		return err
	}

	// err = h.machine.Shutdown(h.ctx)
	// if err != nil {
	// 	h.logger.Error("handle.shutdown: something went wrong while shutting down machine", "error", err)
	// 	return err
	// }

	h.logger.Info("!!!!!!!!!! stopped task")

	return nil
}

func (h *taskHandle) stats(ctx context.Context, statsChannel chan *drivers.TaskResourceUsage, interval time.Duration) {
	defer close(statsChannel)
	timer := time.NewTimer(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			timer.Reset(interval)
		}

		// h.stateLock.Lock()
		// t := time.Now()

		// pid, err := h.machine.PID()
		// if err != nil {
		// 	h.logger.Error("unable get pid", "from", h.taskConfig.ID)
		// 	continue
		// }

		// p, err := process.NewProcess(int32(pid))
		// if err != nil {
		// 	h.logger.Error("unable create new process", "pid", pid, "from", h.taskConfig.ID)
		// 	continue
		// }
		// ms := &drivers.MemoryStats{}
		// if memInfo, err := p.MemoryInfo(); err == nil {
		// 	ms.RSS = memInfo.RSS
		// 	ms.Swap = memInfo.Swap
		// 	ms.Measured = []string{"RSS", "Swap"}
		// }

		// cs := &drivers.CpuStats{}
		// if cpuStats, err := p.Times(); err == nil {
		// 	cs.SystemMode = h.cpuStatsSys.Percent(cpuStats.System * float64(time.Second))
		// 	cs.UserMode = h.cpuStatsUser.Percent(cpuStats.User * float64(time.Second))
		// 	cs.Percent = h.cpuStatsTotal.Percent(cpuStats.Total() * float64(time.Second))
		// 	cs.Measured = []string{"System Mode", "User Mode", "Percent"}

		// }
		// h.stateLock.Unlock()

		// usage := drivers.TaskResourceUsage{
		// 	ResourceUsage: &drivers.ResourceUsage{
		// 		CpuStats:    cs,
		// 		MemoryStats: ms,
		// 	},
		// 	Timestamp: t.UTC().UnixNano(),
		// }
		// // send stats to nomad
		// statsChannel <- &usage
	}
}

func (h *taskHandle) signal(sig os.Signal) error {
	pid, err := h.machine.PID()
	if err != nil {
		return fmt.Errorf("could not find pid: %v+", err)
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("could not find process: %v+", err)
	}

	err = p.Signal(sig)
	if err != nil {
		return fmt.Errorf("could not send signal to process: %v+", err)
	}

	return nil
}
