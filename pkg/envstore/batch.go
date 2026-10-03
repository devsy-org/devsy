package envstore

// BatchStore extends environment CRUD with atomic mutations used when a caller
// must compensate another store's failed operation. Snapshots stay in memory.
type BatchStore interface {
	EnvStore
	DeleteValues(contextName string, names []string) error
	RestoreValues(contextName string, values []EnvValue) error
}

// DeleteValues removes only the requested names in one atomic file replacement.
// On error the existing env file remains unchanged.
func (s *localStore) DeleteValues(contextName string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		if err := ValidateName(name); err != nil {
			return err
		}
	}
	unlock, err := s.locked()
	if err != nil {
		return err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	for _, name := range names {
		delete(d.Contexts[contextName], name)
	}
	if len(d.Contexts[contextName]) == 0 {
		delete(d.Contexts, contextName)
	}
	return s.save(d)
}

// RestoreValues restores values and timestamps without changing other entries.
func (s *localStore) RestoreValues(contextName string, values []EnvValue) error {
	if len(values) == 0 {
		return nil
	}
	for _, value := range values {
		if err := ValidateName(value.Name); err != nil {
			return err
		}
	}
	unlock, err := s.locked()
	if err != nil {
		return err
	}
	defer unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	if d.Contexts[contextName] == nil {
		d.Contexts[contextName] = map[string]EnvValue{}
	}
	for _, value := range values {
		d.Contexts[contextName][value.Name] = value
	}
	return s.save(d)
}
