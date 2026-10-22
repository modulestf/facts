package facts

// applyProfile reads the module profile in Options.ProfileDir into the areas
// that need it. It is a no-op seam today: it reads nothing and sets nothing,
// so no output depends on Options.ProfileDir yet. The profile rules fill it in.
func applyProfile(_ Options, _ []*Area) error { return nil }
