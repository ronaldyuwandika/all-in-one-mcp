# Prompt Polish Task Detection Accuracy Report

Calculated across 200 test prompts.

## Overall Accuracy: 87.50%

## Breakdown by Task Type

| Task Type | Total Prompts | Correct | Accuracy |
| --- | --- | --- | --- |
| Coding | 50 | 45 | 90.00% |
| Agentic | 50 | 40 | 80.00% |
| Analysis | 50 | 45 | 90.00% |
| General | 50 | 45 | 90.00% |

## Sample Mismatches

| Prompt | Expected | Got |
| --- | --- | --- |
| "create unit test for the memory store (id: 8)" | coding | general |
| "create unit test for the memory store (id: 18)" | coding | general |
| "create unit test for the memory store (id: 28)" | coding | general |
| "create unit test for the memory store (id: 38)" | coding | general |
| "create unit test for the memory store (id: 48)" | coding | general |
| "setup ci/cd pipeline for the rust package (id: 4)" | agentic | coding |
| "run container orchestration script on AWS ECS (id: 8)" | agentic | coding |
| "setup ci/cd pipeline for the rust package (id: 14)" | agentic | coding |
| "run container orchestration script on AWS ECS (id: 18)" | agentic | coding |
| "setup ci/cd pipeline for the rust package (id: 24)" | agentic | coding |
