# Consolidation Quality Evaluation Protocol

This document defines the guidelines and scoring criteria for human evaluation of consolidated reasoning patterns. The target metric is to achieve an average score of **>3.5 out of 5.0** across 50 randomly sampled merged patterns.

## Evaluation Guidelines

For each consolidated pattern, the evaluator should compare the merged pattern with the two source episodes (Episode A and Episode B) and rate the consolidation on a scale of **1 to 5** based on the criteria below.

## Scoring Criteria (1-5 Scale)

| Score | Description | Criteria |
| :---: | :--- | :--- |
| **5** | **Excellent** | Perfect consolidation. The merged prompt clearly describes the generalization. The master thinking path has completely merged duplicate steps cleanly without losing any specific techniques from either source episode. |
| **4** | **Good** | Successful consolidation. The consolidated prompt is clean, and the thinking path accurately merges common steps. Minimal redundancy or minor wording duplication in the thinking path. |
| **3** | **Fair** | Acceptable but sub-optimal. The consolidation is accurate, but the thinking trace contains some duplicate steps or lines that should have been pruned or combined. No critical loss of information. |
| **2** | **Poor** | Low quality. Major redundancies in the thinking trace, or the consolidated prompt fails to properly represent the connection between the two episodes. |
| **1** | **Unacceptable** | Failed consolidation. The merged pattern is corrupted, unreadable, or completely unrelated to the source episodes. |

## Grading Steps

1. Open `bench/results/consolidation-quality.md`.
2. For each pattern, read **Source Episode A** and **Source Episode B**.
3. Review the **Consolidated Prompt** and **Master Thinking Path**.
4. Grade the pattern from **1** to **5** using the criteria above.
5. Write your score in the `[ ]` placeholder and compute the overall average.
