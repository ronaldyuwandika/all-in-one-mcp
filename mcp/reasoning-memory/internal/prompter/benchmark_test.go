package prompter

import (
	"strings"
	"testing"
)

func BenchmarkPolishPrompt(b *testing.B) {
	for _, tc := range []struct {
		name      string
		prompt    string
		domain    string
		context   string
		skillName string
	}{
		{
			name:   "coding_simple",
			prompt: "Write a Go function to parse JSON configuration file and validate required fields",
			domain: "",
		},
		{
			name:   "coding_with_context",
			prompt: "Implement a REST API handler for user registration with email verification",
			domain: "coding",
			context: `<reasoning_memory>
  <episode id="1">
    <problem>Design REST API for user management</problem>
    <domain>coding</domain>
    <outcome>success</outcome>
    <tags>go,api,rest</tags>
    <thinking_trace>1. Define endpoints\n2. Implement handlers\n3. Add validation\n4. Write tests</thinking_trace>
  </episode>
</reasoning_memory>`,
		},
		{
			name:      "coding_with_skill",
			prompt:    "Build a Docker image for a Go service with multi-stage builds",
			domain:    "",
			skillName: "docker-expert",
		},
		{
			name:   "agentic_orchestration",
			prompt: "Orchestrate deployment of microservices across Kubernetes clusters with canary rollout",
			domain: "agentic",
		},
		{
			name:   "analysis_debugging",
			prompt: "Analyze why the database connection pool is exhausted under 1000 QPS load",
			domain: "analysis",
		},
		{
			name:   "general_long",
			prompt: strings.Repeat("Explain the trade-offs between microservices and monoliths for a team of 10 developers handling 500k daily active users with PCI compliance requirements. ", 3),
			domain: "",
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				result, err := PolishPrompt(tc.prompt, tc.domain, tc.context, tc.skillName, false)
				if err != nil {
					b.Fatalf("polish: %v", err)
				}
				_ = result.PolishedPrompt
			}
		})
	}
}

func BenchmarkPolishPromptCompact(b *testing.B) {
	for b.Loop() {
		_, err := PolishPrompt("Implement a Go struct parser with reflection", "coding", "", "docker-expert", true)
		if err != nil {
			b.Fatalf("polish: %v", err)
		}
	}
}

func BenchmarkDetectTaskType(b *testing.B) {
	prompts := []string{
		"Implement a Go function to parse JSON and return typed structs",
		"Deploy the application to production with zero downtime and health checks",
		"Analyze why the cache hit ratio dropped from 95% to 60% after the latest deployment",
		"What are the best practices for error handling in Go HTTP servers?",
		"Fix the race condition in the concurrent map access handler",
		"Compare the performance of JSON vs Protocol Buffers for 10KB payloads at 10k RPS",
		"Write a Kubernetes operator that manages PostgreSQL cluster lifecycle",
		"Tell me about your experience with platform engineering",
	}

	for i := 0; i < len(prompts); i++ {
		b.Run("prompt_"+itoa(i+1), func(b *testing.B) {
			for b.Loop() {
				_ = DetectTaskType(prompts[i])
			}
		})
	}
}

func BenchmarkDetectLanguage(b *testing.B) {
	samples := []string{
		"Write a Go function to parse JSON configuration file and validate required fields",
		"Implement a Python decorator that measures function execution time and logs to stdout",
		"Create a Rust trait for serializable configuration with default values",
		"Build a TypeScript type that extracts all nested optional fields from a deep interface",
		"Design a PostgreSQL schema for multi-tenant SaaS with row-level security",
		"Write a Kubernetes deployment manifest for a Go service with health probes",
		"Create a Docker Compose file for a full-stack app with PostgreSQL and Redis and Nginx reverse proxy",
		"Implement a bash script that monitors disk usage and sends alerts via webhook",
	}

	for _, s := range samples {
		b.Run("", func(b *testing.B) {
			for b.Loop() {
				_ = DetectLanguage(s)
			}
		})
	}
}

func BenchmarkBuildXMLEpisodeBlock(b *testing.B) {
	for _, n := range []int{1, 3, 10} {
		eps := make([]EpisodeContext, n)
		for i := 0; i < n; i++ {
			eps[i] = EpisodeContext{
				Problem:       "Problem " + itoa(i+1) + " with sufficient detail for benchmarking the XML building pipeline",
				Domain:        "coding",
				Outcome:       "success",
				Tags:          []string{"go", "benchmark", "xml"},
				ThinkingTrace: strings.Repeat("1. Analyze requirements\n2. Design solution\n3. Implement\n4. Verify\n", 2),
			}
		}

		b.Run("episodes_"+itoa(n), func(b *testing.B) {
			for b.Loop() {
				_ = BuildXMLEpisodeBlock(eps)
			}
		})
	}
}

func BenchmarkLoadSkill(b *testing.B) {
	for b.Loop() {
		data, err := LoadSkill("docker-expert")
		if err != nil {
			b.Fatalf("load skill: %v", err)
		}
		if data != nil {
			_ = BuildSkillContext(data)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
