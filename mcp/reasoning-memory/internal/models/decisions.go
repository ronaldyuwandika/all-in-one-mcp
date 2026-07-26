package models

import "time"

type Alternative struct {
	Name            string   `json:"name" yaml:"name"`
	Description     string   `json:"description,omitempty" yaml:"description,omitempty"`
	RejectionReason string   `json:"rejection_reason" yaml:"rejection_reason"`
	Tradeoffs       []string `json:"tradeoffs,omitempty" yaml:"tradeoffs,omitempty"`
}

type Decision struct {
	ID           string        `json:"id" yaml:"id"`
	EpisodeID    string        `json:"episode_id" yaml:"episode_id"`
	CreatedAt    time.Time     `json:"created_at" yaml:"created_at"`
	Repo         string        `json:"repo" yaml:"repo"`
	Title        string        `json:"title" yaml:"title"`
	Selected     string        `json:"selected" yaml:"selected"`
	Rationale    string        `json:"rationale" yaml:"rationale"`
	Tradeoffs    []string      `json:"tradeoffs,omitempty" yaml:"tradeoffs,omitempty"`
	Assumptions  []string      `json:"assumptions,omitempty" yaml:"assumptions,omitempty"`
	Evidence     []string      `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Alternatives []Alternative `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
}
