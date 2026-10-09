package agent

import (
	"context"
	"fmt"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	"github.com/hollis-labs/torque/internal/launchartifacts"
)

// artifactLaunch reserves only the existing accepted Boot operation. It does
// not authorize project mutations, credentials or a provider process launch.
type artifactLaunch struct {
	compiled *agentlaunch.CompiledLaunch
	digest   string
}

func (m *Manager) beginArtifactLaunch(ctx context.Context, sessionID, controlParent string, compiled *agentlaunch.CompiledLaunch) (launchartifacts.Admission, func(), error) {
	if sessionID == "" || compiled == nil {
		return launchartifacts.Admission{}, nil, fmt.Errorf("missing accepted artifact launch")
	}
	if err := ctx.Err(); err != nil {
		return launchartifacts.Admission{}, nil, err
	}
	digest, err := launchartifacts.Digest(compiled)
	if err != nil {
		return launchartifacts.Admission{}, nil, err
	}
	m.mu.Lock()
	if m.stopped || m.artifactLaunches[sessionID] != nil || m.bootDirs[sessionID] != "" || m.wrapperSessions[sessionID] != nil {
		m.mu.Unlock()
		return launchartifacts.Admission{}, nil, fmt.Errorf("artifact launch session unavailable")
	}
	if m.artifactLaunches == nil {
		m.artifactLaunches = make(map[string]*artifactLaunch)
	}
	launch := &artifactLaunch{compiled: compiled, digest: digest}
	m.artifactLaunches[sessionID] = launch
	m.mu.Unlock()
	validate := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.stopped || m.artifactLaunches[sessionID] != launch {
			return fmt.Errorf("accepted artifact launch retired")
		}
		current, err := launchartifacts.Digest(launch.compiled)
		if err != nil {
			return err
		}
		if current != launch.digest {
			return fmt.Errorf("accepted artifact launch plan changed")
		}
		return nil
	}
	release := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.artifactLaunches[sessionID] == launch {
			delete(m.artifactLaunches, sessionID)
		}
	}
	return launchartifacts.Admission{OperationID: sessionID, DecisionID: "torque.boot:" + sessionID, Version: digest, Owner: "torque", ControlParent: controlParent, LocalFilesystem: true, Validate: validate}, release, nil
}
