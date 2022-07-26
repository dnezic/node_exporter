// Copyright 2017 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !noxfs
// +build !noxfs

package collector

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
)

// An xfsQuotaCollector is a Collector which gathers metrics from XFS filesystems.
type xfsQuotaCollector struct {
	logger log.Logger
}

type Mount struct {
	Device     string
	Path       string
	Filesystem string
	Flags      string
}

type Quota struct {
	Mountpoint string
	User       string
	Used       float64
	SoftLimit  float64
	HardLimit  float64
}

func init() {
	registerCollector("xfs_quota", defaultEnabled, NewXFSQuotaCollector)
}

// NewXFSCollector returns a new Collector exposing XFS statistics.
func NewXFSQuotaCollector(logger log.Logger) (Collector, error) {

	return &xfsQuotaCollector{
		logger: logger,
	}, nil
}

func isXfs(xfsPath string) (bool, error) {
	cmd := exec.Command("stat", "-f", "-c", "%T", xfsPath)
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(out)) != "xfs" {
		return false, nil
	}
	return true, nil
}

func isXfsQuota(Flags string) bool {
	return !strings.Contains(Flags, "noquota")
}

func Mounts() ([]Mount, error) {
	file, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	defer closeFile(file)
	mounts := []Mount(nil)
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				return mounts, nil
			}
			return nil, err
		}
		if isPrefix {
			return nil, syscall.EIO
		}
		parts := strings.SplitN(string(line), " ", 5)
		if len(parts) != 5 {
			return nil, syscall.EIO
		}
		mounts = append(mounts, Mount{parts[0], parts[1], parts[2], parts[3]})
	}

}

func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		fmt.Errorf("failed to close /proc/self/mounts: %v", err)
	}
}

// Update implements Collector.
func (c *xfsQuotaCollector) Update(ch chan<- prometheus.Metric) error {

	c.updateXFSQuotaStats(ch)

	return nil
}

func toFloat64(word string) float64 {
	v, _ := strconv.ParseFloat(word, 64)
	return float64(v)
}

// updateXFSStats collects statistics for a single XFS filesystem.
func (c *xfsQuotaCollector) updateXFSQuotaStats(ch chan<- prometheus.Metric) {
	const (
		subsystem = "xfs_quota"
	)

	mounts, _ := Mounts()

	xfs_mounts := mounts[:0]
	for _, mount := range mounts {
		// fmt.Println(i, mount.Path, mount.Filesystem, mount.Device)
		var _isXfs, _ = isXfs(mount.Path)
		if _isXfs && isXfsQuota(mount.Flags) {
			xfs_mounts = append(xfs_mounts, mount)
		}
	}

	mounts = xfs_mounts
	quotas := []Quota(nil)

	for _, mount := range mounts {
		cmd := exec.Command("xfs_quota", "-x", "-c", "report -gpu -b -N", mount.Path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("xfs_quota failed with error: %v, output: %s", err, out)
		}
		// fmt.Printf("xfs_quota: %s: output: %s", mount.Path, out)
		parts := strings.SplitN(string(out), "\n", 100)
		// fmt.Printf("xfs_quota: %s", parts)
		for _, part := range parts {
			words := strings.Fields(part)
			// fmt.Printf("xfs_quota words: %s", words)
			if len(words) >= 4 {
				var user = words[0]
				usage := float64(toFloat64(words[1]))
				squota := float64(toFloat64(words[2]))
				hquota := float64(toFloat64(words[3]))
				quotas = append(quotas, Quota{mount.Path, user, usage, squota, hquota})
			}

		}
	}

	//fmt.Printf("xfs_quota quotas: %v", quotas)

	for _, q := range quotas {

		desc_used_bytes := prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "xfs_quota_used_bytes"),
			"Used bytes by the user at the mountpoint",
			[]string{"mountpoint", "user"},
			nil,
		)

		ch <- prometheus.MustNewConstMetric(
			desc_used_bytes,
			prometheus.GaugeValue,
			q.Used,
			q.Mountpoint,
			q.User,
		)

		desc_soft_limit := prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "xfs_quota_soft_limit_bytes"),
			"Soft limit for the user for the mountpoint",
			[]string{"mountpoint", "user"},
			nil,
		)

		ch <- prometheus.MustNewConstMetric(
			desc_soft_limit,
			prometheus.GaugeValue,
			q.SoftLimit,
			q.Mountpoint,
			q.User,
		)

		desc_hard_limit := prometheus.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "xfs_quota_hard_limit_bytes"),
			"Hard limit for the user for the mountpoint",
			[]string{"mountpoint", "user"},
			nil,
		)

		ch <- prometheus.MustNewConstMetric(
			desc_hard_limit,
			prometheus.GaugeValue,
			q.HardLimit,
			q.Mountpoint,
			q.User,
		)

	}
}
