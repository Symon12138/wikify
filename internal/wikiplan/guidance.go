package wikiplan

import "strings"

// GuidanceText formats template + notes for injection into the catalog agent prompt.
// Returns empty string when the plan has nothing useful to say.
func (p *Plan) GuidanceText() string {
	if p == nil || p.Wiki == nil {
		return ""
	}
	var lines []string
	if t := strings.TrimSpace(p.Wiki.Template); t != "" {
		lines = append(lines, "Template: "+t)
	}
	for _, n := range p.Wiki.Notes {
		text := strings.TrimSpace(n.Text)
		if text == "" {
			continue
		}
		if a := strings.TrimSpace(n.Author); a != "" {
			lines = append(lines, "- ("+a+") "+text)
		} else {
			lines = append(lines, "- "+text)
		}
	}
	if p.Scope != nil {
		if len(p.Scope.Include) > 0 {
			lines = append(lines, "Scope include: "+strings.Join(p.Scope.Include, ", "))
		}
		if len(p.Scope.Exclude) > 0 {
			lines = append(lines, "Scope exclude: "+strings.Join(p.Scope.Exclude, ", "))
		}
	}
	return strings.Join(lines, "\n")
}
