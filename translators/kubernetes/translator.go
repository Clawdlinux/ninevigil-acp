// SPDX-License-Identifier: Apache-2.0
// Package kubernetes translates Kubernetes API objects into Agent Native Format.
//
// This package is designed to be imported by agentic-operator-core:
//
//	import "github.com/Clawdlinux/agent-native-format/translators/kubernetes"
//
// The translator takes standard k8s.io types and produces ANF documents
// that compress ~12,000 tokens of raw K8s JSON into ~350 tokens of
// agent-native representation.
package kubernetes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Clawdlinux/agent-native-format/pkg/anf"
)

const TranslatorVersion = "clawdlinux/k8s-translator:0.1.0"

// NamespaceView is the input to the translator: pre-fetched K8s objects
// for a single namespace. This avoids coupling to client-go directly,
// making the translator testable with plain structs.
type NamespaceView struct {
	Cluster     string
	Namespace   string
	Deployments []Deployment
	Services    []Service
	Jobs        []Job
	CronJobs    []CronJob
	Events      []Event
	// AgentPermissions controls which ?actions are emitted.
	AgentPermissions Permissions
}

type Deployment struct {
	Name           string
	Replicas       int32
	ReadyReplicas  int32
	Image          string
	Strategy       string
	MaxSurge       string
	MaxUnavailable string
	AgeDays        int
	CPUPercent     int
	MemPercent     int
	RequestsPerSec float64
	ErrorRate      float64
	Pods           []Pod
	LatestImage    string // if known, for outdated-image detection
}

type Pod struct {
	Name        string
	Phase       string // Running, Pending, Failed, Succeeded
	Node        string
	CPUPercent  int
	MemPercent  int
	Restarts    int32
	Restarts24h int32
	AgeDays     int
}

type Service struct {
	Name       string
	Type       string // ClusterIP, NodePort, LoadBalancer
	Port       int32
	TargetPort int32
	Endpoints  int32
	TotalPods  int32
}

type Job struct {
	Name      string
	Completed bool
	LastRun   time.Time
	Duration  time.Duration
	Succeeded bool
}

type CronJob struct {
	Name     string
	Schedule string
	LastRun  time.Time
	NextRun  time.Time
}

type Event struct {
	Type     string // Normal, Warning
	Reason   string
	Object   string // "Deployment/payment-api"
	Message  string
	Count    int32
	LastSeen time.Time
}

type Permissions struct {
	CanScale    bool
	CanRollout  bool
	CanRestart  bool
	CanLogs     bool
	CanExec     bool
	CanDescribe bool
}

// Translate converts a NamespaceView into an ANF document.
func Translate(view NamespaceView, now time.Time) *anf.Document {
	doc := anf.NewDocument(
		fmt.Sprintf("kubernetes/%s", view.Cluster),
		fmt.Sprintf("namespace:%s", view.Namespace),
		now,
	)
	doc.SetTTL(60)
	doc.SetTranslator(TranslatorVersion)

	// Entities: deployments with child pods
	for _, dep := range view.Deployments {
		doc.AddEntity(translateDeployment(dep))
	}

	// Entities: services
	for _, svc := range view.Services {
		doc.AddEntity(translateService(svc))
	}

	// Entities: jobs
	for _, job := range view.Jobs {
		doc.AddEntity(translateJob(job))
	}

	// Entities: cronjobs
	for _, cj := range view.CronJobs {
		doc.AddEntity(translateCronJob(cj))
	}

	// Alerts: derived from state
	alerts := deriveAlerts(view)
	for _, a := range alerts {
		doc.AddAlert(a)
	}

	// Actions: based on permissions + current state
	actions := deriveActions(view)
	for _, a := range actions {
		doc.AddAction(a)
	}

	return doc
}

func translateDeployment(dep Deployment) anf.Entity {
	status := deploymentStatus(dep)

	inline := []anf.Property{
		{Key: "replicas", Value: fmt.Sprintf("%d/%d", dep.ReadyReplicas, dep.Replicas)},
	}
	if dep.AgeDays > 0 {
		inline = append(inline, anf.Property{Key: "age", Value: fmt.Sprintf("%dd", dep.AgeDays)})
	}

	props := []anf.Property{
		{Key: "image", Value: dep.Image},
	}
	if dep.Strategy != "" {
		stratLine := dep.Strategy
		if dep.MaxSurge != "" {
			stratLine += " maxSurge:" + dep.MaxSurge
		}
		if dep.MaxUnavailable != "" {
			stratLine += " maxUnavail:" + dep.MaxUnavailable
		}
		props = append(props, anf.Property{Key: "strategy", Value: stratLine})
	}

	// Resource usage line
	usageParts := []string{}
	if dep.CPUPercent > 0 {
		usageParts = append(usageParts, fmt.Sprintf("cpu %d%%", dep.CPUPercent))
	}
	if dep.MemPercent > 0 {
		usageParts = append(usageParts, fmt.Sprintf("mem %d%%", dep.MemPercent))
	}
	if dep.RequestsPerSec > 0 {
		usageParts = append(usageParts, fmt.Sprintf("requests:%s/s", formatCompactNumber(dep.RequestsPerSec)))
	}
	if dep.ErrorRate > 0 {
		usageParts = append(usageParts, fmt.Sprintf("errors:%.2f%%", dep.ErrorRate))
	}
	if len(usageParts) > 0 {
		props = append(props, anf.Property{Key: strings.Join(usageParts[:1], ""), Value: strings.Join(usageParts[1:], " ")})
	}

	// Child pods
	children := make([]anf.Entity, 0, len(dep.Pods))
	for _, pod := range dep.Pods {
		children = append(children, translatePod(pod))
	}

	return anf.Entity{
		Type:        "deployment",
		Name:        dep.Name,
		Status:      status,
		InlineProps: inline,
		Props:       props,
		Children:    children,
	}
}

func translatePod(pod Pod) anf.Entity {
	status := podStatus(pod)

	inline := []anf.Property{}
	if pod.Node != "" {
		inline = append(inline, anf.Property{Key: "node", Value: pod.Node})
	}
	if pod.CPUPercent > 0 {
		inline = append(inline, anf.Property{Key: "cpu", Value: fmt.Sprintf("%d%%", pod.CPUPercent)})
	}
	if pod.MemPercent > 0 {
		inline = append(inline, anf.Property{Key: "mem", Value: fmt.Sprintf("%d%%", pod.MemPercent)})
	}
	inline = append(inline, anf.Property{Key: "restarts", Value: fmt.Sprintf("%d", pod.Restarts)})

	return anf.Entity{
		Type:        "pod",
		Name:        pod.Name,
		Status:      status,
		InlineProps: inline,
	}
}

func translateService(svc Service) anf.Entity {
	inline := []anf.Property{}
	if svc.Port > 0 && svc.TargetPort > 0 {
		inline = append(inline, anf.Property{Key: "ports", Value: fmt.Sprintf("%d>%d", svc.Port, svc.TargetPort)})
	}
	if svc.TotalPods > 0 {
		inline = append(inline, anf.Property{Key: "endpoints", Value: fmt.Sprintf("%d/%d", svc.Endpoints, svc.TotalPods)})
	}

	return anf.Entity{
		Type:        "service",
		Name:        svc.Name,
		Status:      anf.StatusEmpty,
		InlineProps: inline,
		Props: []anf.Property{
			{Key: "type", Value: svc.Type},
		},
	}
}

func translateJob(job Job) anf.Entity {
	status := anf.StatusCompleted
	if !job.Completed {
		status = anf.StatusRunning
	}
	if !job.Succeeded && job.Completed {
		status = anf.StatusFailing
	}

	inline := []anf.Property{}
	if !job.LastRun.IsZero() {
		inline = append(inline, anf.Property{Key: "last", Value: job.LastRun.Format(time.RFC3339)})
	}
	if job.Duration > 0 {
		inline = append(inline, anf.Property{Key: "duration", Value: formatDurationCompact(job.Duration)})
	}

	return anf.Entity{
		Type:        "job",
		Name:        job.Name,
		Status:      status,
		InlineProps: inline,
	}
}

func translateCronJob(cj CronJob) anf.Entity {
	inline := []anf.Property{
		{Key: "schedule", Value: cj.Schedule},
	}
	if !cj.LastRun.IsZero() {
		inline = append(inline, anf.Property{Key: "last-run", Value: cj.LastRun.Format(time.RFC3339)})
	}
	if !cj.NextRun.IsZero() {
		inline = append(inline, anf.Property{Key: "next", Value: cj.NextRun.Format(time.RFC3339)})
	}

	return anf.Entity{
		Type:        "cronjob",
		Name:        cj.Name,
		Status:      anf.StatusEmpty,
		InlineProps: inline,
	}
}

// deriveAlerts examines state and generates alerts.
func deriveAlerts(view NamespaceView) []anf.Alert {
	var alerts []anf.Alert

	for _, dep := range view.Deployments {
		// High memory on deployment
		if dep.MemPercent > 80 {
			alerts = append(alerts, anf.Alert{
				Severity: "warning",
				Type:     "deployment",
				Name:     dep.Name,
				Message:  fmt.Sprintf("mem:%d%%", dep.MemPercent),
				Props:    map[string]string{"threshold": "80%"},
			})
		}
		// High CPU
		if dep.CPUPercent > 80 {
			alerts = append(alerts, anf.Alert{
				Severity: "warning",
				Type:     "deployment",
				Name:     dep.Name,
				Message:  fmt.Sprintf("cpu:%d%%", dep.CPUPercent),
				Props:    map[string]string{"threshold": "80%"},
			})
		}
		// Replicas not ready
		if dep.ReadyReplicas < dep.Replicas {
			alerts = append(alerts, anf.Alert{
				Severity: "critical",
				Type:     "deployment",
				Name:     dep.Name,
				Message:  fmt.Sprintf("replicas:%d/%d", dep.ReadyReplicas, dep.Replicas),
			})
		}
		// Image outdated
		if dep.LatestImage != "" && dep.LatestImage != dep.Image {
			alerts = append(alerts, anf.Alert{
				Severity: "info",
				Type:     "deployment",
				Name:     dep.Name,
				Message:  "image-outdated",
				Props: map[string]string{
					"current": dep.Image,
					"latest":  dep.LatestImage,
				},
			})
		}

		// Pod-level alerts
		for _, pod := range dep.Pods {
			if pod.Restarts24h > 2 {
				alerts = append(alerts, anf.Alert{
					Severity: "warning",
					Type:     "pod",
					Name:     pod.Name,
					Message:  fmt.Sprintf("restarts:%d/24h", pod.Restarts24h),
					Props:    map[string]string{"threshold": "2"},
				})
			}
			if pod.MemPercent > 90 {
				alerts = append(alerts, anf.Alert{
					Severity: "critical",
					Type:     "pod",
					Name:     pod.Name,
					Message:  fmt.Sprintf("mem:%d%%", pod.MemPercent),
					Props:    map[string]string{"threshold": "90%"},
				})
			}
		}
	}

	// Sort: critical first, then warning, then info
	sort.Slice(alerts, func(i, j int) bool {
		return severityRank(alerts[i].Severity) < severityRank(alerts[j].Severity)
	})

	return alerts
}

// deriveActions computes available operations from permissions and state.
func deriveActions(view NamespaceView) []anf.Action {
	var actions []anf.Action

	for _, dep := range view.Deployments {
		if view.AgentPermissions.CanScale {
			actions = append(actions, anf.Action{
				Verb: "scale",
				Type: "deployment",
				Name: dep.Name,
				Params: map[string]string{
					"current": fmt.Sprintf("%d", dep.Replicas),
					"range":   "1-10",
				},
			})
		}
		if view.AgentPermissions.CanRollout && dep.LatestImage != "" && dep.LatestImage != dep.Image {
			actions = append(actions, anf.Action{
				Verb: "rollout",
				Type: "deployment",
				Name: dep.Name,
				Params: map[string]string{
					"to":       dep.LatestImage,
					"strategy": dep.Strategy,
				},
			})
		}
		if view.AgentPermissions.CanRestart {
			actions = append(actions, anf.Action{
				Verb: "restart",
				Type: "deployment",
				Name: dep.Name,
			})
		}

		// Pod-level actions
		for _, pod := range dep.Pods {
			if view.AgentPermissions.CanLogs {
				actions = append(actions, anf.Action{
					Verb: "logs",
					Type: "pod",
					Name: pod.Name,
					Params: map[string]string{
						"since": "1h",
					},
				})
			}
		}
	}

	return actions
}

// Status derivation helpers

func deploymentStatus(dep Deployment) anf.Status {
	if dep.ReadyReplicas == 0 && dep.Replicas > 0 {
		return anf.StatusFailing
	}
	if dep.ReadyReplicas < dep.Replicas {
		return anf.StatusDegraded
	}
	if dep.MemPercent > 80 || dep.CPUPercent > 80 {
		return anf.StatusDegraded
	}
	for _, pod := range dep.Pods {
		if pod.Restarts24h > 2 || pod.MemPercent > 90 {
			return anf.StatusDegraded
		}
	}
	return anf.StatusHealthy
}

func podStatus(pod Pod) anf.Status {
	switch strings.ToLower(pod.Phase) {
	case "running":
		return anf.StatusRunning
	case "pending":
		return anf.StatusPending
	case "failed":
		return anf.StatusFailing
	case "succeeded":
		return anf.StatusCompleted
	default:
		return anf.StatusUnknown
	}
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

func formatCompactNumber(n float64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", n/1_000)
	case n == float64(int(n)):
		return fmt.Sprintf("%d", int(n))
	default:
		return fmt.Sprintf("%.1f", n)
	}
}

func formatDurationCompact(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
