package plugin

// ScopedInteropPrincipal describes the case attached by the authenticated portal
// router. HandleRoute checks the grant's installation and route before dispatch.
func (req RouteRequest) ScopedInteropPrincipal() (string, string, bool) {
	g := req.Grant
	if g == nil || !g.Valid() || req.Viewer == nil || req.Viewer.User.ID == "" {
		return "", "", false
	}
	return g.Kind, g.ID, true
}
