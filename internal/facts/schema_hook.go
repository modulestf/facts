package facts

// applySchema reads the schema handoff and the schema files named in o into
// the areas that need them. It is a no-op seam today: it reads nothing and
// sets nothing, so no output depends on Options.SchemaHandoff or
// Options.SchemaFiles yet. The schema rules fill it in.
func applySchema(_ Options, _ []*Area) error { return nil }
