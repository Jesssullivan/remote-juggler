# Multi-Agent Plan Development: Research Notes

**Date**: 2026-02-27
**Purpose**: Inform the 6-week RemoteJuggler Agent Plane epic
**Research scope**: Multi-agent coordination, planning patterns, communication protocols, self-improvement loops, formal verification, production deployment patterns

---

## Table of Contents

1. [Key Patterns Discovered](#1-key-patterns-discovered)
2. [Agent Communication Protocols](#2-agent-communication-protocols)
3. [The OODA Loop Problem and Solutions](#3-the-ooda-loop-problem-and-solutions)
4. [Self-Evolving Agent Architecture](#4-self-evolving-agent-architecture)
5. [Production Multi-Agent Deployment Patterns](#5-production-multi-agent-deployment-patterns)
6. [Formal Verification in Agent Systems](#6-formal-verification-in-agent-systems)
7. [Applicable Frameworks for 6-Week Epic Structure](#7-applicable-frameworks-for-6-week-epic-structure)
8. [Specific Recommendations for RemoteJuggler](#8-specific-recommendations-for-remotejuggler)
9. [Sources and Citations](#9-sources-and-citations)

---

## 1. Key Patterns Discovered

### 1.1 The Three Engineering Patterns for Reliable Multi-Agent Workflows (GitHub)

GitHub's engineering team published a definitive guide identifying that **most multi-agent workflow failures come from missing structure, not model capability**. Three patterns make the difference:

**Pattern 1: Typed Schemas**
Every agent boundary must enforce machine-checkable data contracts. Invalid messages fail fast. No handoff without validation. Implementation uses discriminated unions and strict type checking:

```typescript
type UserProfile = {
  id: number;
  email: string;
  plan: "free" | "pro" | "enterprise";
};
```

**Pattern 2: Action Schemas**
LLMs follow explicit instructions, not implied intent. Define the exact set of permitted outcomes. Agent must return exactly one valid action from a constrained set:

```typescript
const ActionSchema = z.discriminatedUnion("type", [
  { type: "request-more-info", missing: string[] },
  { type: "assign", assignee: string },
  { type: "close-as-duplicate", duplicateOf: number },
  { type: "no-action" }
]);
```

**Pattern 3: MCP as Enforcement Layer**
MCP validates tool calls *before* execution, preventing agents from inventing fields, omitting required inputs, or drifting across interfaces. Bad state never reaches production.

> **Key quote**: "Treat agents like distributed system components, not chat flows."

**Source**: [GitHub Blog: Multi-agent workflows often fail](https://github.blog/ai-and-ml/generative-ai/multi-agent-workflows-often-fail-heres-how-to-engineer-ones-that-dont/)

### 1.2 Cybernetic Recursion: The Agent Loop Architecture

An agent comprises four architectural components:

| Component | Role |
|-----------|------|
| Probabilistic Reasoning Core | LLM generates hypotheses and plans |
| Deterministic Control Loop | Code managing execution flow |
| Environmental Interface | Tools for sensing and acting |
| Memory Subsystem | State persistence across time |

> **Key quote**: "A smarter model in a broken loop is a chatbot; a weaker model in a robust loop is an agent."

The operational cycle follows **Sense-Think-Act-Observe**:
- **Sense**: Context engineering via recursive summarization and RAG
- **Think**: Chain of Thought with structured JSON outputs
- **Act**: Tool invocation via JSON schemas (dual-purpose as validation and semantic prompts)
- **Observe**: Reality-testing feedback loops enabling self-correction

Memory tiers:
- **Short-Term**: Immediate conversation history
- **Long-Term**: Vector databases (embeddings)
- **Episodic**: Discrete event storage for historical recall
- **Procedural**: Standardized runbooks retrieved contextually

**Source**: [The Cybernetic Recursion: Architectures, Dynamics, and Engineering of AI Agent Loops](https://atlassc.net/2026/02/13/cybernetic-recursion-ai-agent-loops)

### 1.3 Microsoft Multi-Agent Reference Architecture (10 Patterns)

Microsoft documents 10 production patterns:

1. **Semantic Router with LLM Fallback** -- Lightweight classifiers first, expensive LLMs only when confidence is low
2. **Dynamic Agent Registry** -- Agents self-register with capability descriptors, enabling runtime discovery
3. **Semantic Kernel Orchestration** -- Chains reusable skills with memory, planning, and goal alignment
4. **Local & Remote Agent Execution** -- Supervisors coordinate both local and distributed agents
5. **Agent-to-Agent Communication** -- Three sub-patterns: orchestrator-mediated, direct peer, pub/sub
6. **Skill Chaining with Planning** -- Automatic composition of stateless capabilities into multi-step workflows
7. **Layered (Onion) Architecture** -- Separates Orchestration, Agent, Knowledge, Storage, Integration layers
8. **MCP Integration Layer** -- Decouples tool invocation, adds governance and auditability
9. **RAG** -- Grounds responses using vector-stored documents
10. **Conversation-Aware Orchestration** -- Memory context for personalization across interactions

**Source**: [Microsoft Multi-Agent Reference Architecture](https://microsoft.github.io/multi-agent-reference-architecture/docs/reference-architecture/Patterns.html)

### 1.4 Deloitte Three-Layer Enterprise Architecture

Deloitte proposes three architectural layers for agent orchestration:

**Context Layer**: Knowledge graphs, ontologies, domain taxonomies, optimized context retrieval

**Agent Layer**: Modular design with tool relevance/abstraction, memory optimization, model selection, security, telemetry

**Experience Layer**: Agent status dashboards, prompt suggestions, explainability, error recovery

Guardian agents function as a specialized governance layer -- sensing risky behaviors, managing other agent execution, owning specific tasks while overseeing orchestration. Projected 10-15% of agentic AI market by 2030.

**Source**: [Deloitte: Unlocking exponential value with AI agent orchestration](https://www.deloitte.com/us/en/insights/industry/technology/technology-media-and-telecom-predictions/2026/ai-agent-orchestration.html)

---

## 2. Agent Communication Protocols

### 2.1 Protocol Landscape (2025-2026)

A comprehensive survey (arXiv:2505.02279) compares four emerging protocols:

| Protocol | Creator | Model | Transport | Security |
|----------|---------|-------|-----------|----------|
| **MCP** | Anthropic (Nov 2024) | Client-server, JSON-RPC | HTTP, Stdio, SSE | OAuth 2.1, input sanitization |
| **ACP** | IBM Research | REST-native performative | HTTP with streams | Mutual TLS, JWS signing |
| **A2A** | Google Cloud (Apr 2025) | Peer-to-peer task delegation | HTTP, SSE, Push | DID handshakes, signed Agent Cards |
| **ANP** | Open community | Decentralized network | HTTPS, JSON-LD | DID:wba, cryptographic auth |

### 2.2 Recommended Phased Adoption Roadmap

The survey recommends a staged approach:

1. **Stage 1 -- MCP**: Tool invocation and structured LLM-external service integration (JSON-RPC primitives)
2. **Stage 2 -- ACP**: Asynchronous multimodal messaging for richer conversations
3. **Stage 3 -- A2A**: Enterprise multi-agent workflows via capability cards and artifact-driven task delegation
4. **Stage 4 -- ANP**: Open-internet agent discovery with decentralized identity verification

### 2.3 Communication Patterns

From the Microsoft reference architecture and industry research, three dominant patterns:

**Orchestrator-Mediated Routing**: Central orchestrator decomposes requests and routes to specialists. Best for controlled, auditable workflows.

**Direct Peer Communication**: Agents communicate directly with notification back to orchestrator. Best for low-latency, high-trust environments.

**Pub/Sub Messaging**: Agents publish to topics and subscribe to relevant channels via event bus. Best for loose coupling, scalability, resilience.

**Message-Driven Architecture** (Microsoft): Agents interact asynchronously via broker/event bus. The orchestrator decomposes requests into subtasks, issues command messages, and specialized agents publish responses to reply-to queues.

**Source**: [arXiv: Survey of Agent Interoperability Protocols](https://arxiv.org/html/2505.02279v1)

### 2.4 Typed Schemas as Communication Foundation

From GitHub's research and the broader community: **typed schemas are table stakes**. Without them, nothing else works. Multi-agent workflows fail when agents exchange messy language or inconsistent JSON, with field names changing and data types not matching.

**Agent Context Protocol (ACP)** formalizes this with three structured message formats: `ASSISTANCE_REQUEST`, `AGENT_REQUEST`, and `AGENT_RESPONSE`.

---

## 3. The OODA Loop Problem and Solutions

### 3.1 Schneier's OODA Loop Analysis

Bruce Schneier (Harvard Berkman Klein Center, October 2025) identifies fundamental vulnerabilities in the agent OODA loop:

**Observe-phase risks**: Adversarial examples, prompt injection, sensor spoofing. The observation layer lacks authentication and integrity.

**Orient-phase risks**: Training data poisoning, context manipulation, semantic backdoors. Attackers can influence the model's worldview months before deployment.

**Decide-phase risks**: AI decides probabilistically without verification of its observations.

**Act-phase risks**: Actions executed without integrity checks propagate errors.

> **Key quote**: "Integrity isn't a feature you add; it's an architecture you choose."

MCP and similar tool-calling systems create nested OODA loops where each tool has its own loop that nests, interleaves, and races. Tool descriptions become injection vectors. Models cannot verify tool semantics, only syntax.

**Source**: [Schneier on Security: Agentic AI's OODA Loop Problem](https://www.schneier.com/blog/archives/2025/10/agentic-ais-ooda-loop-problem.html)

### 3.2 NVIDIA LLo11yPop: Closing the OODA Loop in Production

NVIDIA's observability AI agent framework demonstrates a working production OODA loop:

**Architecture**: Mixture of Agents (MoA) approach -- multiple small, focused LLMs chained for specific tasks (SQL query generation, metric analysis, etc.)

**Supervisor Agent**: Operates autonomously within an OODA loop -- observing telemetry data, orienting, deciding, and acting (opening Jira tickets, calling PagerDuty).

**Human Oversight**: Forms a reinforcement learning loop -- human verification of agent actions feeds back into the system.

**Outcome**: Improved GPU cluster reliability through autonomous monitoring with escalation.

**Source**: [NVIDIA Technical Blog: Optimizing Data Center Performance with AI Agents and the OODA Loop Strategy](https://developer.nvidia.com/blog/optimizing-data-center-performance-with-ai-agents-and-the-ooda-loop-strategy/)

### 3.3 Bounded Autonomy Spectrum

Deloitte and Gartner converge on a three-tier autonomy model:

| Level | Model | When to Use |
|-------|-------|-------------|
| Human-in-the-Loop | Active human involvement per decision | High-stakes, early deployment |
| Human-on-the-Loop | Humans monitor and intervene selectively | Routine tasks, established patterns |
| Humans-out-of-the-Loop | Fully autonomous with continuous monitoring | Low-risk, well-validated domains |

Production systems must implement:
- Clear operational limits (bounded autonomy)
- Mandatory escalation paths for high-stakes decisions
- Comprehensive audit trails
- Guardian/governance agents monitoring other agents

**Source**: [Gartner: 40% of Enterprise Apps Will Feature AI Agents by 2026](https://www.gartner.com/en/newsroom/press-releases/2025-08-26-gartner-predicts-40-percent-of-enterprise-apps-will-feature-task-specific-ai-agents-by-2026-up-from-less-than-5-percent-in-2025)

---

## 4. Self-Evolving Agent Architecture

### 4.1 OpenAI Self-Evolving Agents Cookbook

OpenAI published a production-ready pattern for autonomous agent retraining:

**Four-Stage Retraining Loop**:
1. Baseline Agent produces initial outputs
2. Feedback Collection via human review or LLM-as-judge
3. Evals & Scoring measure against defined criteria
4. Updated Baseline replaces original when improvements exceed threshold

**Three Prompt Optimization Strategies** (escalating sophistication):

| Strategy | Method | Speed | Human Effort |
|----------|--------|-------|--------------|
| Manual UI-Based | Visual interfaces, human annotation | Slow | High |
| LLM-as-Judge Automation | Meta-prompting agent generates revisions from grader feedback | Medium | Low |
| Aggregate Selection + Versioning | Multi-test-case tracking, highest-scoring candidate selection | Fast | Minimal |

**LLM-as-Judge Eval Pattern** uses four complementary graders:
- Chemical Name Grader (Python): Verifies domain entities
- Length Deviation Grader (Python): Enforces target word counts
- Cosine Similarity (Text): Anchors to source content
- Score Model (LLM): Rubric-driven nuanced quality evaluation

**Lenient Pass Logic**: A candidate advances if either 75% of graders pass OR average score exceeds 0.85.

**Versioned Prompt Tracking**: `VersionedPrompt` class maintains historical records with metadata, timestamps, eval IDs. Enables rollback and production observability.

**Source**: [OpenAI Cookbook: Self-Evolving Agents](https://developers.openai.com/cookbook/examples/partners/self_evolving_agents/autonomous_agent_retraining)

### 4.2 ICLR 2026 Workshop on AI with Recursive Self-Improvement

The ICLR 2026 workshop (accepted) focuses on:
- Architectures for recursive self-improvement
- Safety constraints on self-modifying systems
- Evaluation frameworks for evolving agents

This signals the academic community recognizes self-improvement as a first-class research area.

**Source**: [ICLR 2026 Workshop](https://openreview.net/pdf?id=OsPQ6zTQXV)

### 4.3 Voyager Pattern: Skills Library Evolution

The Voyager agent (Minecraft, 2023) established a pattern still relevant today:
- Iteratively prompt LLM for code
- Refine code based on environment feedback
- Store working programs in an expanding skills library
- Compose existing skills for new, more complex tasks

This is the "procedural memory" tier in modern agent architectures.

**Source**: [The Cybernetic Recursion (Atlas SC)](https://atlassc.net/2026/02/13/cybernetic-recursion-ai-agent-loops)

---

## 5. Production Multi-Agent Deployment Patterns

### 5.1 Framework Landscape (2025-2026)

| Framework | Pattern | Strengths | Production Status |
|-----------|---------|-----------|-------------------|
| **LangGraph** | State machines, graph execution | Time-travel debugging, human-in-the-loop | ~600-800 companies (est. end 2025) |
| **AutoGen** | Social conversation between agents | Flexible agent topologies | Active production use |
| **CrewAI** | Role-based collaborative teams | Hierarchical task management | Growing adoption |
| **Google ADK** | Multi-agent with A2A support | Google Cloud integration | New entrant |
| **OpenAI Agents SDK** | Lightweight multi-agent | Native OpenAI integration | Active |

### 5.2 Graph Topology Research

From Orogat et al. (Feb 2026): **Pipeline topologies collapse at scale**, but fully connected and scale-free graphs maintain high coordination success.

This means linear agent chains (A -> B -> C) break down as complexity increases. Hub-and-spoke or mesh topologies are more resilient.

**Source**: [Emergent Mind: Multi-agent LLM Frameworks](https://www.emergentmind.com/topics/multi-agent-llm-frameworks)

### 5.3 Production Deployment Lessons

From multiple sources:

**Design for failure first**: Assume agents will make mistakes. Build recovery mechanisms into every workflow.

**Validate every boundary**: No handoff without validation. Schema violations are contract failures.

**Constrain before scaling**: Add agents only when existing constraints work reliably.

**Log intermediate state**: Enable debugging of multi-step workflows. Event sourcing recommended for complex workflows.

**Circuit breakers**: Protect against cascading failures in agent communication.

**Async processing**: Design agents for asynchronous message handling to improve responsiveness.

### 5.4 Market Data

- Gartner: 40% of enterprise apps will embed AI agents by end of 2026 (up from <5% in 2025)
- Gartner: >40% of agentic AI projects could be cancelled by 2027 (cost, complexity, risk)
- Deloitte: Autonomous agent market could reach $45B by 2030
- 1,445% surge in multi-agent system inquiries Q1 2024 to Q2 2025
- Google Cloud study: 88% of early adopters achieved positive ROI

---

## 6. Formal Verification in Agent Systems

### 6.1 The Verification Imperative

Martin Kleppmann (December 2025) predicts AI will bring formal verification mainstream. The term "vericoding" describes using LLMs to generate formally verified code.

Proof-oriented languages relevant to agent systems:
- **F\*** (F-star): Dependently-typed, effect-tracking, proof generation
- **Lean**: Mathematical proof assistant with growing AI integration
- **Isabelle/HOL**: Classical theorem prover
- **Agda**: Dependently-typed functional language

Startups making progress: Harmonic's Aristotle, Logical Intelligence, DeepSeek-Prover-V2.

**Source**: [Kleppmann: AI will make formal verification go mainstream](https://martin.kleppmann.com/2025/12/08/ai-formal-verification.html)

### 6.2 Dhall for Policy Configuration

Dhall provides total-function, typed configuration language ideal for:
- Agent policy definitions (what agents can/cannot do)
- Non-Turing-complete guarantees (policies always terminate)
- Type-safe composition of policy rules
- Reproducible, auditable policy generation

### 6.3 NIST AI Agent Security (January 2026)

NIST's Center for AI Safety and Innovation (CAISI) issued an RFI on securing AI agent systems, signaling regulatory attention to:
- Agent authentication and authorization
- Decision audit trails
- Input/output integrity verification
- Tool access control policies

**Source**: [NIST: CAISI Issues RFI About Securing AI Agent Systems](https://www.nist.gov/news-events/news/2026/01/caisi-issues-request-information-about-securing-ai-agent-systems)

### 6.4 Relevance to RemoteJuggler's HexStrike-AI

HexStrike-AI already uses F\* verification for its 42 MCP tools and Dhall for policy definitions. This positions it ahead of the curve relative to industry trends. The formal verification approach provides:
- Proven tool correctness before deployment
- Policy decisions that always terminate (Dhall totality)
- Auditable grant/deny decisions
- Type-safe policy composition

---

## 7. Applicable Frameworks for 6-Week Epic Structure

### 7.1 BMAD Method (Build More Architect Dreams)

The BMAD Method is an open-source AI-driven agile framework specifically designed for AI agent development with a four-phase cycle:

**Phase 1 -- Analysis**: BMAD Analyst agent gathers requirements, identifies constraints
**Phase 2 -- Planning**: PM agent creates comprehensive PRD
**Phase 3 -- Solutioning**: Architect agent designs architecture, patterns, interfaces
**Phase 4 -- Implementation**: Scrum Master shards the PRD into focused, self-contained stories

**Epic Sharding**: The comprehensive PRD is systematically broken into hyper-detailed story files containing:
- Full architectural context
- Implementation guidelines
- Embedded reasoning (rationale)
- Testing criteria for quality assurance

> **Key insight**: BMAD solves the "context loss" problem that plagues AI-driven development by making each story self-contained.

**Source**: [BMAD Method](https://github.com/bmad-code-org/BMAD-METHOD), [BMAD Docs](http://docs.bmad-method.org/)

### 7.2 Mission-Driven Development (6-Week Cycle)

Toptal's Mission-Driven Development framework uses 6-week cycles as the "Goldilocks of product timeframes":

- Enough time to deliver a Minimum Viable Product
- Short enough to maintain urgency and focus
- Structured around missions (comparable to epics) with clear success criteria
- Each mission has a dedicated team with full ownership

**Source**: [Toptal: Mission Driven Development and the 6-Week Cycle](https://www.toptal.com/project-managers/agile/mission-driven-development-6-week-cycle)

### 7.3 Recommended 6-Week Epic Structure for Agent Platform

Based on synthesized research, here is a phased approach:

#### Week 1-2: Foundation and Contracts
- Define typed schemas for all agent-to-agent and agent-to-gateway communication
- Formalize action schemas (what each agent can do/return)
- Establish MCP enforcement at every boundary
- Set up observability (OODA Observe phase infrastructure)
- Define campaign protocol v2 with structured message types

#### Week 3-4: Loop Closure and Self-Improvement
- Implement the observe-orient-decide-act loop for each agent type
- Add LLM-as-judge eval graders for campaign outcomes
- Build feedback collection pipeline (GitHub issues as structured feedback)
- Implement versioned prompt/config tracking for agent evolution
- Add guardian/governance agent pattern for cross-agent monitoring

#### Week 5: Integration and Hardening
- End-to-end testing of multi-agent campaigns with typed schemas
- Circuit breaker and retry patterns for agent communication failures
- Audit trail verification
- Human escalation path testing
- Policy verification (F\*/Dhall) for HexStrike grants

#### Week 6: Production and Measurement
- Deploy improvements to cluster
- Measure campaign success rates against baseline
- Document self-improvement loop results
- Identify next-cycle improvements
- Publish metrics dashboard (Experience Layer)

---

## 8. Specific Recommendations for RemoteJuggler

### 8.1 Immediate Priorities (Weeks 1-2)

**R1: Formalize Campaign Protocol with Typed Schemas**

The current campaign dispatch uses ad-hoc JSON. Based on GitHub's research, formalize every boundary:

```typescript
// Campaign Request Schema
type CampaignDispatch = {
  type: "campaign_dispatch";
  campaign_id: string;
  agent: "ironclaw" | "picoclaw" | "hexstrike-ai" | "gateway-direct";
  session_key: string;
  payload: CampaignPayload;
  timeout_ms: number;
};

// Campaign Result Schema (Action Schema pattern)
type CampaignResult =
  | { type: "finding"; severity: "low" | "medium" | "high" | "critical"; detail: string }
  | { type: "no-finding"; evidence: string }
  | { type: "error"; code: string; message: string }
  | { type: "escalate"; reason: string; context: object };
```

**R2: MCP Enforcement at Gateway Boundary**

The gateway already validates tool schemas. Extend this to campaign dispatch and results:
- Validate campaign payloads before forwarding to agents
- Validate agent results before writing to audit log
- Reject malformed responses with structured error feedback

**R3: Agent Registry with Capability Cards**

Adopt A2A-style Agent Cards for runtime discovery:
- Each agent self-describes capabilities on /health or /api/tools
- Gateway maintains registry of active agents and their tool sets
- Campaigns matched to agents via capability intersection

### 8.2 Loop Closure (Weeks 3-4)

**R4: Implement Campaign OODA Loop**

Following NVIDIA's LLo11yPop pattern:

```
OBSERVE: Campaign runner monitors agent heartbeats, tool traces, Aperture metrics
ORIENT:  Aggregate findings across campaigns, detect patterns (repeated failures, common vulnerabilities)
DECIDE:  Determine next campaign priorities, identify agents needing configuration changes
ACT:     Trigger follow-up campaigns, create GitHub issues for findings, adjust agent configs
```

**R5: Self-Improvement via GitHub Issues as Structured Feedback**

GitHub Issues already serve as the finding output channel. Structure this as a feedback loop:
1. Agent produces finding -> creates structured GitHub issue
2. Human (or governance agent) reviews -> adds labels/comments
3. Feedback aggregated -> informs next campaign parameters
4. Campaign runner adjusts scheduling, priorities, or agent prompts

This aligns with OpenAI's self-evolving agent pattern: capture edge cases, learn from feedback, promote improvements.

**R6: Versioned Campaign Configurations**

Track campaign config versions with metadata:
- Config hash, timestamp, eval results
- Rollback capability if new config degrades performance
- A/B testing of campaign variants

### 8.3 Governance and Safety (Week 5)

**R7: Guardian Agent Pattern**

Implement a lightweight governance agent (could run as a gateway-direct campaign):
- Monitor campaign execution across all agents
- Detect anomalies (unusually long runs, excessive tool calls, repeated failures)
- Escalate to human via GitHub issue with full context
- Enforce bounded autonomy: agents have clear operational limits

**R8: Extend HexStrike Dhall Policy Engine**

The existing Dhall policy engine is ahead of industry trends. Extend it:
- Add the 4 missing tool grants (`network_posture`, `api_fuzz`, `sops_rotation_check`, `cve_monitor`)
- Use F\* proofs to verify policy completeness
- Export policy as human-readable audit document
- Consider making policy engine reusable across agents

### 8.4 Communication Protocol Evolution (Ongoing)

**R9: Follow the Phased Protocol Roadmap**

RemoteJuggler is already at **Stage 1** (MCP for tool access). Plan for:

- **Stage 1 (current)**: MCP -- tool invocation, structured data exchange (JSON-RPC over stdio/HTTP)
- **Stage 2 (this epic)**: Structured campaign messages with typed schemas (comparable to ACP)
- **Stage 3 (next epic)**: Agent-to-agent task delegation (comparable to A2A Agent Cards)
- **Stage 4 (future)**: Open network agent discovery (if the platform expands beyond the cluster)

**R10: Adopt Message-Driven Async Pattern**

The Microsoft reference architecture's message-driven model maps well to RemoteJuggler:
- Campaign runner as orchestrator publishes to agents
- Agents process asynchronously, publish results
- Gateway aggregates and routes
- Aperture provides the observability/metering layer

### 8.5 Metrics and Evaluation

**R11: Define Campaign Success Metrics**

Following OpenAI's eval pattern:
- **Finding quality score**: LLM-as-judge rates finding severity, accuracy, actionability
- **False positive rate**: Track findings that get closed without action
- **Campaign completion rate**: Successful runs / total runs
- **Time to finding**: Seconds from campaign trigger to issue creation
- **Agent evolution score**: Improvement in success metrics over time (per config version)

---

## 9. Sources and Citations

### Primary Research Papers

1. [LLM-Coordination: Evaluating and Analyzing Multi-agent Coordination Abilities in Large Language Models](https://arxiv.org/abs/2310.03903) -- NAACL 2025, Agashe et al.
2. [Multi-Agent Collaboration Mechanisms: A Survey of LLMs](https://arxiv.org/html/2501.06322v1) -- Jan 2025
3. [A Survey of Agent Interoperability Protocols: MCP, ACP, A2A, ANP](https://arxiv.org/html/2505.02279v1) -- arXiv 2505.02279, May 2025
4. [LLM-Based Multi-Agent Systems for Software Engineering](https://dl.acm.org/doi/10.1145/3712003) -- ACM TOSEM
5. [A survey on LLM-based multi-agent systems: workflow, infrastructure, and challenges](https://link.springer.com/article/10.1007/s44336-024-00009-2) -- Springer Nature
6. [Large Language Model Agents: A Comprehensive Survey](https://www.preprints.org/manuscript/202512.2119) -- Dec 2025

### Industry Reports and Analysis

7. [Deloitte: Unlocking exponential value with AI agent orchestration](https://www.deloitte.com/us/en/insights/industry/technology/technology-media-and-telecom-predictions/2026/ai-agent-orchestration.html) -- TMT Predictions 2026
8. [Gartner: 40% of Enterprise Apps Will Feature AI Agents by 2026](https://www.gartner.com/en/newsroom/press-releases/2025-08-26-gartner-predicts-40-percent-of-enterprise-apps-will-feature-task-specific-ai-agents-by-2026-up-from-less-than-5-percent-in-2025) -- Aug 2025
9. [WMAC 2026: AAAI Bridge on Advancing LLM-Based Multi-Agent Collaboration](https://multiagents.org/2026/)
10. [NIST CAISI: Securing AI Agent Systems](https://www.nist.gov/news-events/news/2026/01/caisi-issues-request-information-about-securing-ai-agent-systems) -- Jan 2026

### Technical Guides and Production Patterns

11. [GitHub Blog: Multi-agent workflows often fail. Here's how to engineer ones that don't.](https://github.blog/ai-and-ml/generative-ai/multi-agent-workflows-often-fail-heres-how-to-engineer-ones-that-dont/) -- Gwen Davis
12. [Microsoft Multi-Agent Reference Architecture](https://microsoft.github.io/multi-agent-reference-architecture/docs/reference-architecture/Patterns.html) -- GitHub
13. [NVIDIA: Optimizing Data Center Performance with AI Agents and the OODA Loop](https://developer.nvidia.com/blog/optimizing-data-center-performance-with-ai-agents-and-the-ooda-loop-strategy/) -- LLo11yPop
14. [OpenAI Cookbook: Self-Evolving Agents](https://developers.openai.com/cookbook/examples/partners/self_evolving_agents/autonomous_agent_retraining)
15. [The Cybernetic Recursion: Architectures, Dynamics, and Engineering of AI Agent Loops](https://atlassc.net/2026/02/13/cybernetic-recursion-ai-agent-loops) -- Feb 2026

### Security and Verification

16. [Schneier: Agentic AI's OODA Loop Problem](https://www.schneier.com/blog/archives/2025/10/agentic-ais-ooda-loop-problem.html) -- Oct 2025, Harvard Berkman Klein
17. [Kleppmann: AI will make formal verification go mainstream](https://martin.kleppmann.com/2025/12/08/ai-formal-verification.html) -- Dec 2025
18. [ICLR 2026 Workshop on AI with Recursive Self-Improvement](https://openreview.net/pdf?id=OsPQ6zTQXV)

### Frameworks and Methodologies

19. [BMAD Method: Breakthrough Method for Agile AI-Driven Development](https://github.com/bmad-code-org/BMAD-METHOD) -- Open source
20. [Toptal: Mission Driven Development and the 6-Week Cycle](https://www.toptal.com/project-managers/agile/mission-driven-development-6-week-cycle)

### Agent Communication Protocols

21. [Model Context Protocol (MCP) -- Wikipedia](https://en.wikipedia.org/wiki/Model_Context_Protocol)
22. [MCP vs A2A: Protocols for Multi-Agent Collaboration 2026](https://onereach.ai/blog/guide-choosing-mcp-vs-a2a-protocols/)
23. [Top 5 Open Protocols for Building Multi-Agent AI Systems 2026](https://onereach.ai/blog/power-of-multi-agent-ai-open-protocols/)
24. [Auth0: MCP vs A2A: A Guide to AI Agent Communication Protocols](https://auth0.com/blog/mcp-vs-a2a/)

### OpenClaw and Tool Integration

25. [OpenClaw MCP Server (freema/openclaw-mcp)](https://github.com/freema/openclaw-mcp)
26. [OpenClaw Claude Code Skill](https://github.com/Enderfga/openclaw-claude-code-skill)
27. [SafeClaw: How to Use MCP With OpenClaw](https://safeclaw.io/blog/openclaw-mcp)
28. [Why CLIs Beat MCP for AI Agents](https://medium.com/@rentierdigital/why-clis-beat-mcp-for-ai-agents-and-how-to-build-your-own-cli-army-6c27b0aec969)

### Additional Multi-Agent System References

29. [Emergent Mind: Multi-agent LLM Frameworks](https://www.emergentmind.com/topics/multi-agent-llm-frameworks)
30. [Confluent: Four Design Patterns for Event-Driven Multi-Agent Systems](https://www.confluent.io/blog/event-driven-multi-agent-systems/)
31. [The Agentic Shift: 2025 Progress and 2026 Trends](https://medium.com/@huguosuo/the-agentic-shift-2025-progress-and-2026-trends-in-autonomous-ai-d8248b57ade9)
32. [Agents At Work: The 2026 Playbook for Building Reliable Agentic Workflows](https://promptengineering.org/agents-at-work-the-2026-playbook-for-building-reliable-agentic-workflows/)

---

## Appendix: Key Terminology

| Term | Definition |
|------|-----------|
| **OODA Loop** | Observe-Orient-Decide-Act decision framework (Boyd) |
| **MCP** | Model Context Protocol -- Anthropic's agent-to-tool standard |
| **A2A** | Agent-to-Agent Protocol -- Google's peer delegation standard |
| **ACP** | Agent Communication Protocol -- IBM's REST-native messaging |
| **ANP** | Agent Network Protocol -- Decentralized agent discovery |
| **Bounded Autonomy** | Agents operate within defined limits with escalation paths |
| **Guardian Agent** | Governance agent monitoring other agents for policy violations |
| **Epic Sharding** | BMAD pattern for breaking PRDs into self-contained stories |
| **Agent Card** | A2A capability descriptor for runtime agent discovery |
| **Vericoding** | Using LLMs to generate formally verified code |
| **GEPA** | Genetic-Pareto optimization for multi-objective prompt refinement |
| **MoA** | Mixture of Agents -- multiple specialized LLMs chained together |
