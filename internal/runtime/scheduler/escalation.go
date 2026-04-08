package scheduler

// EscalationAction is the result of consulting the escalation chain.
type EscalationAction struct {
	Strategy  string // "retry", "senior-agent", "council", "human", "block"
	NextStep  int    // The escalation step to record on the task
	Exhausted bool   // True when the chain has been fully walked
}

// EscalationResolution describes the state changes to apply after an escalation action.
type EscalationResolution struct {
	NewStatus          string
	NewEscalationStep  int
	BlockedReason      string
	ChangeAgentProfile bool
	AgentProfile       string
}

// EscalationEngine walks a task's escalation chain on failure.
type EscalationEngine struct{}

// NewEscalationEngine creates a new escalation engine.
func NewEscalationEngine() *EscalationEngine {
	return &EscalationEngine{}
}

// NextAction returns the next escalation action given the chain and current step.
// If the chain is exhausted, it returns a "block" action.
func (e *EscalationEngine) NextAction(chain []string, currentStep int) EscalationAction {
	if len(chain) == 0 || currentStep >= len(chain) {
		return EscalationAction{
			Strategy:  "block",
			NextStep:  currentStep,
			Exhausted: true,
		}
	}

	return EscalationAction{
		Strategy:  chain[currentStep],
		NextStep:  currentStep + 1,
		Exhausted: false,
	}
}

// Resolve converts an escalation action into concrete state changes.
func (e *EscalationEngine) Resolve(action EscalationAction) (*EscalationResolution, error) {
	switch action.Strategy {
	case "retry":
		return &EscalationResolution{
			NewStatus:         "todo",
			NewEscalationStep: action.NextStep,
		}, nil

	case "senior-agent":
		return &EscalationResolution{
			NewStatus:          "todo",
			NewEscalationStep:  action.NextStep,
			ChangeAgentProfile: true,
			AgentProfile:       "senior",
		}, nil

	case "council":
		return &EscalationResolution{
			NewStatus:          "todo",
			NewEscalationStep:  action.NextStep,
			ChangeAgentProfile: true,
			AgentProfile:       "council",
		}, nil

	case "human":
		return &EscalationResolution{
			NewStatus:         "blocked",
			NewEscalationStep: action.NextStep,
			BlockedReason:     "Escalated to human review",
		}, nil

	case "block":
		return &EscalationResolution{
			NewStatus:         "blocked",
			NewEscalationStep: action.NextStep,
			BlockedReason:     "Escalation chain exhausted",
		}, nil

	default:
		// Unknown strategy — treat as block
		return &EscalationResolution{
			NewStatus:         "blocked",
			NewEscalationStep: action.NextStep,
			BlockedReason:     "Unknown escalation strategy: " + action.Strategy,
		}, nil
	}
}
