package logic

import "strings"

func DefaultMaxTurns() int { return 40 }

func CanWritePlan(role string) bool { return role == "b" }

func PlanRoots(scratch string, touched []string) []string { return []string{scratch} }

func AbortRecordRole() string { return "system" }

func ShouldDeleteWorkingLog(relative string) bool { return strings.HasSuffix(strings.ToLower(relative), ".jsonl") }
