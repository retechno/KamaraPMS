package auditpack

import "context"

// Override replaces the builder of a section, so that a test can make it fail.
func (s *Service) Override(code string, f func(ctx context.Context) (any, error)) {
	if s.override == nil {
		s.override = map[string]func(ctx context.Context) (any, error){}
	}
	s.override[code] = f
}
