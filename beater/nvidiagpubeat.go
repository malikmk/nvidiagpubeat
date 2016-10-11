package beater

import (
	"bytes"
	"fmt"
	"time"
	"encoding/xml"
	"strings"
	"strconv"

	"os/exec"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/publisher"

	"github.com/zen-chetan/nvidiagpubeat/config"
)

type NvidiaSmiOutput struct {
	Timestamp string `xml:"timestamp"`
	DriverVersion string `xml:"driver_version"`
	AttachedGPUs int `xml:"attached_gpus"`
	GPUs []GPUInfo `xml:"gpu"`
}

type GPUInfo struct {
	Id string `xml:"id,attr"`
	FrameBufferTotalMemory string `xml:"fb_memory_usage>total"`
	FrameBufferFreeMemory string `xml:"fb_memory_usage>free"`
	FrameBufferUsedMemory string `xml:"fb_memory_usage>used"`
	Bar1TotalMemory string `xml:"bar1_memory_usage>total"`
	Bar1FreeMemory string `xml:"bar1_memory_usage>free"`
	Bar1UsedMemory string `xml:"bar1_memory_usage>used"`
	GPUUtilization string `xml:"utilization>gpu_util"`
	MemoryUtilization string `xml:"utilization>memory_util"`
	Processes []ProcessInfo `xml:"processes>process_info"`
}

type ProcessInfo struct {
	Pid string `xml:"pid"`
	Process string `xml:"process_name"`
	UsedMemory string `xml:"used_memory"`
}

type Nvidiagpubeat struct {
	done   chan struct{}
	config config.Config
	client publisher.Client
}

// Creates beater
func New(b *beat.Beat, cfg *common.Config) (beat.Beater, error) {
	config := config.DefaultConfig
	if err := cfg.Unpack(&config); err != nil {
		return nil, fmt.Errorf("Error reading config file: %v", err)
	}

	bt := &Nvidiagpubeat{
		done: make(chan struct{}),
		config: config,
	}
	return bt, nil
}

func Split(text string) (float64, string) {
	parts := strings.SplitN(text, " ", 2)
	value, _ := strconv.ParseFloat(parts[0], 64)
	units := parts[1]
	return value, units
}

func RunNvidiaSmi(b *beat.Beat) ([]common.MapStr) {
	cmd := exec.Command("nvidia-smi", "-q", "-x")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		logp.Err("Failed to run nvidia-smi!")
		return nil
	}

	var v NvidiaSmiOutput
	err2 := xml.Unmarshal(out.Bytes(), &v)
	if err2 != nil {
		logp.Err("Failed to bind to output of nvidia-smi!")
		return nil
	}

	var events []common.MapStr

	for _, gpu_info := range v.GPUs {
		fb_total, _ := Split(gpu_info.FrameBufferTotalMemory)
		fb_free, _ := Split(gpu_info.FrameBufferFreeMemory)
		fb_used, _ := Split(gpu_info.FrameBufferUsedMemory)
		b_total, _ := Split(gpu_info.Bar1TotalMemory)
		b_free, _ := Split(gpu_info.Bar1FreeMemory)
		b_used, _ := Split(gpu_info.Bar1UsedMemory)
		gpu_utilization, _ := Split(gpu_info.GPUUtilization)
		memory_utilization, _ := Split(gpu_info.MemoryUtilization)
		event := common.MapStr {
			"@timestamp": common.Time(time.Now()),
			"type": "gpu",
			"gpu": common.MapStr {
				"id": gpu_info.Id,
				"frame_buffer_total_mb": fb_total,
				"frame_buffer_free_mb": fb_free,
				"frame_buffer_used_mb": fb_used,
				"bar1_total_mb": b_total,
				"bar1_free_mb": b_free,
				"bar1_used_mb": b_used,
				"processsor_utilization_pct": gpu_utilization,
				"memory_utilization_pct": memory_utilization,
				"process_count": len(gpu_info.Processes),
			},
		}
		events = append(events, event)

		for _, process_info := range gpu_info.Processes {
			used_memory, _ := Split(process_info.UsedMemory)
			pevent := common.MapStr {
				"@timestamp": common.Time(time.Now()),
				"type": "gpu_process",
				"gpu_process": common.MapStr {
					"gpu_id": gpu_info.Id,
					"process_id": process_info.Pid,
					"process": process_info.Process,
					"memory_used_mb": used_memory,
				},
			}
			events = append(events, pevent)
		}
	}

	return events
}

func (bt *Nvidiagpubeat) Run(b *beat.Beat) error {
	logp.Info("nvidiagpubeat is running! Hit CTRL-C to stop it.")

	bt.client = b.Publisher.Connect()
	ticker := time.NewTicker(bt.config.Period)

	for {
		select {
		case <-bt.done:
			return nil
		case <-ticker.C:
		}

		events := RunNvidiaSmi(b)

		for _, event := range events {
			bt.client.PublishEvent(event)
			logp.Info("GPU metric event sent")
		}
	}
}

func (bt *Nvidiagpubeat) Stop() {
	bt.client.Close()
	close(bt.done)
}
