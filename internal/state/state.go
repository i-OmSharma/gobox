package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	StateDir  = "/var/lib/veil"
	StateFile = "state.json"
)

type Container struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Image     string    `json:"image"`
	Command   []string  `json:"command"`
	Status    string    `json:"status"` // running, exited
	Created   time.Time `json:"created"`
	RootFS    string    `json:"rootfs"`
}

type State struct {
	Containers map[string]*Container `json:"containers"`
}

func ensureDir() error {
	return os.MkdirAll(StateDir, 0755)
}

func statePath() string {
	return filepath.Join(StateDir, StateFile)
}

func Load() (*State, error) {
	if err := ensureDir(); err != nil {
		return nil, err
	}
	
	data, err := os.ReadFile(statePath())
	if os.IsNotExist(err) {
		return &State{Containers: make(map[string]*Container)}, nil
	}
	if err != nil {
		return nil, err
	}
	
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Containers == nil {
		s.Containers = make(map[string]*Container)
	}
	return &s, nil
}

func (s *State) Save() error {
	if err := ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), data, 0644)
}

func (s *State) Add(c *Container) error {
	s.Containers[c.ID] = c
	return s.Save()
}

func (s *State) Remove(id string) error {
	delete(s.Containers, id)
	return s.Save()
}

func (s *State) List() []*Container {
	var list []*Container
	for _, c := range s.Containers {
		// Update status if process is dead
		if c.Status == "running" {
			if proc, err := os.FindProcess(c.PID); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					c.Status = "exited"
				}
			}
		}
		list = append(list, c)
	}
	return list
}

func Stop(id string) error {
	s, err := Load()
	if err != nil {
		return err
	}
	// Support prefix matching so short IDs from `veil ps` work.
	c, ok := s.Containers[id]
	if !ok {
		for fullID, container := range s.Containers {
			if len(id) <= len(fullID) && fullID[:len(id)] == id {
				c = container
				ok = true
				break
			}
		}
	}
	if !ok {
		return fmt.Errorf("container not found: %s", id)
	}
	if c.Status != "running" {
		return fmt.Errorf("container not running: %s", c.Status)
	}
	proc, err := os.FindProcess(c.PID)
	if err != nil {
		return err
	}
	// Graceful stop: SIGTERM, then SIGKILL after 5s
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	// Note: In real impl, you'd wait here or use a goroutine
	// For simplicity, we just send the signal
	return s.Save()
}

// Helper to create a new container record
func NewContainer(id, image, rootfs string, command []string, pid int) *Container {
	return &Container{
		ID:      id,
		PID:     pid,
		Image:   image,
		Command: command,
		Status:  "running",
		Created: time.Now(),
		RootFS:  rootfs,
	}
}