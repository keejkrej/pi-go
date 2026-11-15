// Ported from packages/durable/src/harness/registry.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"regexp"
	"sync"
)

var hrSectionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// BuiltinTasks returns the built-in task definitions held by every registry.
// They are not part of any extension and cannot be removed or replaced.
var BuiltinTasks = []AnyTask{GenerationTask, ToolTask, CompactionTask}

type hrRegistryState struct {
	extensions []*Extension
	byName     map[string]*Extension
	tools      []RegistryTool
	sections   []RegistrySection
	taskList   []AnyTask
	tasks      map[string]AnyTask
}

func hrNewRegistryState(extensions []*Extension) (*hrRegistryState, error) {
	extCopy := append([]*Extension(nil), extensions...)
	byName := make(map[string]*Extension, len(extCopy))
	tasks := make(map[string]AnyTask, len(BuiltinTasks))
	var taskList []AnyTask
	for _, t := range BuiltinTasks {
		name := t.TaskKind()
		tasks[name] = t
		taskList = append(taskList, t)
	}
	var tools []RegistryTool
	var sections []RegistrySection
	for _, ext := range extCopy {
		if ext == nil {
			continue
		}
		byName[ext.Name] = ext
		for _, tool := range ext.Tools {
			tools = append(tools, RegistryTool{Extension: ext, Tool: tool})
		}
		for _, sec := range ext.Sections {
			sections = append(sections, RegistrySection{Extension: ext, Section: sec})
		}
		for _, task := range ext.Tasks {
			name := task.TaskKind()
			if _, exists := tasks[name]; exists {
				return nil, fmt.Errorf("Task %s of extension %s is already installed", name, ext.Name)
			}
			tasks[name] = task
			taskList = append(taskList, task)
		}
	}
	return &hrRegistryState{
		extensions: extCopy,
		byName:     byName,
		tools:      tools,
		sections:   sections,
		taskList:   taskList,
		tasks:      tasks,
	}, nil
}

func (s *hrRegistryState) Installed() []*Extension {
	return append([]*Extension(nil), s.extensions...)
}

func (s *hrRegistryState) Extension(name string) *Extension {
	return s.byName[name]
}

func (s *hrRegistryState) Tools() []RegistryTool {
	return append([]RegistryTool(nil), s.tools...)
}

func (s *hrRegistryState) Sections() []RegistrySection {
	return append([]RegistrySection(nil), s.sections...)
}

func (s *hrRegistryState) Tasks() []AnyTask {
	return append([]AnyTask(nil), s.taskList...)
}

func (s *hrRegistryState) Task(name string) AnyTask {
	return s.tasks[name]
}

type hrRegistryImpl struct {
	mu        sync.RWMutex
	current   *hrRegistryState
	listeners map[uint64]func()
	nextId    uint64
}

// CreateRegistry creates an application-owned registry initially holding only the built-in tasks.
func CreateRegistry() Registry {
	state, err := hrNewRegistryState(nil)
	if err != nil {
		panic(err)
	}
	return &hrRegistryImpl{
		current:   state,
		listeners: make(map[uint64]func()),
	}
}

func (r *hrRegistryImpl) Snapshot() RegistrySnapshot {
	r.mu.RLock()
	cur := r.current
	r.mu.RUnlock()
	return cur
}

func (r *hrRegistryImpl) Subscribe(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextId++
	id := r.nextId
	r.listeners[id] = listener
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

func (r *hrRegistryImpl) Install(extension *Extension) error {
	if extension == nil {
		return fmt.Errorf("extension must not be nil")
	}
	if err := hrValidateExtension(extension); err != nil {
		return err
	}
	r.mu.Lock()
	cur := r.current.extensions
	idx := -1
	for i, existing := range cur {
		if existing != nil && existing.Name == extension.Name {
			idx = i
			break
		}
	}
	var next []*Extension
	if idx < 0 {
		next = make([]*Extension, len(cur)+1)
		copy(next, cur)
		next[len(cur)] = extension
	} else {
		next = make([]*Extension, len(cur))
		copy(next, cur)
		next[idx] = extension
	}
	state, err := hrNewRegistryState(next)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.current = state
	listeners := make([]func(), 0, len(r.listeners))
	for _, fn := range r.listeners {
		listeners = append(listeners, fn)
	}
	r.mu.Unlock()

	for _, fn := range listeners {
		fn()
	}
	return nil
}

func (r *hrRegistryImpl) Uninstall(extension *Extension) {
	if extension == nil {
		return
	}
	r.mu.Lock()
	cur := r.current.extensions
	found := false
	for _, existing := range cur {
		if existing != nil && existing.Name == extension.Name {
			found = true
			break
		}
	}
	if !found {
		r.mu.Unlock()
		return
	}
	next := make([]*Extension, 0, len(cur))
	for _, existing := range cur {
		if existing != nil && existing.Name != extension.Name {
			next = append(next, existing)
		}
	}
	state, err := hrNewRegistryState(next)
	if err != nil {
		r.mu.Unlock()
		return
	}
	r.current = state
	listeners := make([]func(), 0, len(r.listeners))
	for _, fn := range r.listeners {
		listeners = append(listeners, fn)
	}
	r.mu.Unlock()

	for _, fn := range listeners {
		fn()
	}
}

func hrValidateExtension(extension *Extension) error {
	tools := make(map[string]struct{}, len(extension.Tools))
	for _, tool := range extension.Tools {
		if tool == nil {
			continue
		}
		if _, dup := tools[tool.Name]; dup {
			return fmt.Errorf("Extension %s has two tools named %s", extension.Name, tool.Name)
		}
		tools[tool.Name] = struct{}{}
	}
	sections := make(map[string]struct{}, len(extension.Sections))
	for _, sec := range extension.Sections {
		if sec == nil {
			continue
		}
		if !hrSectionKeyPattern.MatchString(sec.Key) {
			return fmt.Errorf("Section key %q must match %s", sec.Key, hrSectionKeyPattern.String())
		}
		if sec.Key == InstructionsKey {
			return fmt.Errorf("Section key %s is reserved for the agent's instructions", sec.Key)
		}
		if _, dup := sections[sec.Key]; dup {
			return fmt.Errorf("Extension %s has two sections with key %s", extension.Name, sec.Key)
		}
		sections[sec.Key] = struct{}{}
	}
	return nil
}
