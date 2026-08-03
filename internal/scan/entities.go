package scan

import (
	"regexp"
	"strings"
)

// Entity is a data-bearing type declaration extracted from a source file by
// lightweight per-language line regexes (same style as symbols.go — no AST
// dependency). Kind "entity" means the type carries persistence markers
// (Java @TableName/@Table/@Entity, Go gorm/db struct tags); Kind "class" /
// "interface" are plain declarations kept for class diagrams.
//
// Coverage: Java and Go are extracted in full (fields, columns, keys, table
// names). TypeScript gets a basic interface-field pass. Python and other
// languages are intentionally skipped — their field declarations are not
// reliably extractable with line regexes (assignments in __init__ etc.), and
// a wrong field list is worse than none.
type Entity struct {
	File   string        `json:"file"`
	Name   string        `json:"name"`
	Table  string        `json:"table,omitempty"`
	Kind   string        `json:"kind"` // entity | class | interface
	Fields []EntityField `json:"fields,omitempty"`
}

// EntityField is one field/column of an Entity. Column is the mapped DB or
// serialized name when a tag/annotation declares one; Key marks primary keys.
type EntityField struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Column string `json:"column,omitempty"`
	Key    bool   `json:"key,omitempty"`
}

const (
	maxEntitiesPerFile = 8
	maxEntityFields    = 24
	maxEntitiesTotal   = 240
)

var (
	// Java class-level annotations carrying a table name.
	reEntJavaTableName = regexp.MustCompile(`@TableName\s*\(\s*(?:value\s*=\s*)?"([^"]+)"`)
	reEntJavaTable     = regexp.MustCompile(`@Table\s*\(\s*(?:name\s*=\s*)?"([^"]+)"`)
	reEntJavaEntity    = regexp.MustCompile(`^\s*@Entity\b`)
	// Java type declaration (mirrors reSymJavaType; enum fields are constants
	// so enums are skipped here).
	reEntJavaType = regexp.MustCompile(`^\s*(?:(?:public|protected|private|abstract|final|static|sealed)\s+)*(class|interface|record)\s+([A-Za-z_]\w*)`)
	// Java field: modifiers captured separately so static constants can be
	// skipped; requires a terminating ';' on the same line.
	reEntJavaField = regexp.MustCompile(`^\s*(?:private|protected)\s+((?:(?:static|final|transient|volatile)\s+)*)([\w.<>\[\], ?]+?)\s+([A-Za-z_]\w*)\s*(?:=[^;]*)?;`)
	// Java field-level annotations.
	reEntJavaID     = regexp.MustCompile(`^\s*@(?:TableId|Id)\b`)
	reEntJavaColumn = regexp.MustCompile(`@(?:TableField|Column)\s*\(\s*(?:(?:value|name)\s*=\s*)?"([^"]+)"`)

	// Go struct declaration / field / TableName() method.
	reEntGoStruct    = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+struct\s*\{`)
	reEntGoField     = regexp.MustCompile("^\\s+([A-Za-z_]\\w*)\\s+([^\\s`]+)")
	reEntGoTag       = regexp.MustCompile("`([^`]*)`")
	reEntGoTableName = regexp.MustCompile(`^func\s*\(\s*\*?\s*\w*\s*\*?\s*([A-Za-z_]\w*)\s*\)\s*TableName\s*\(\s*\)\s*string`)
	reEntGoReturnStr = regexp.MustCompile(`return\s+"([^"]+)"`)

	// TS interface declaration / simple field line.
	reEntTSInterface = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?interface\s+([A-Za-z_$][\w$]*)`)
	reEntTSField     = regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Za-z_$][\w$]*)\??\s*:\s*([^;,{}]+?)\s*[;,]?\s*$`)

	reTagGorm = regexp.MustCompile(`gorm:"([^"]*)"`)
	reTagDB   = regexp.MustCompile(`db:"([^"]*)"`)
	reTagJSON = regexp.MustCompile(`json:"([^"]*)"`)
)

// extractEntities returns capped entity/class declarations with their fields
// for the given file extension. Line-regex based (same trade-offs as
// extractSymbols): multi-line declarations may be missed — advisory data only.
func extractEntities(ext, rel, text string) []Entity {
	switch ext {
	case ".java":
		return extractJavaEntities(rel, text)
	case ".go":
		return extractGoEntities(rel, text)
	case ".ts", ".tsx":
		return extractTSEntities(rel, text)
	}
	return nil
}

func extractJavaEntities(rel, text string) []Entity {
	var out []Entity
	var cur *Entity
	pendingTable := ""
	pendingEntity := false
	pendingKey := false
	pendingColumn := ""
	flush := func() {
		if cur == nil {
			return
		}
		if len(cur.Fields) > 0 || cur.Table != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, ln := range strings.Split(text, "\n") {
		if len(out) >= maxEntitiesPerFile {
			break
		}
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") {
			continue
		}
		if m := reEntJavaTableName.FindStringSubmatch(ln); m != nil {
			pendingTable = m[1]
			continue
		}
		if reEntJavaEntity.MatchString(ln) {
			pendingEntity = true
			// @Entity may share the line with nothing else; a name-bearing
			// @Table can still follow on later lines.
			continue
		}
		if cur == nil {
			// @Table only counts before the class declaration (field-level
			// @Column shares no prefix ambiguity: matched below when cur != nil).
			if m := reEntJavaTable.FindStringSubmatch(ln); m != nil {
				pendingTable = m[1]
				pendingEntity = true
				continue
			}
		}
		if m := reEntJavaType.FindStringSubmatch(ln); m != nil {
			flush()
			kind := "class"
			if m[1] == "interface" {
				kind = "interface"
			}
			if pendingTable != "" || pendingEntity {
				kind = "entity"
			}
			cur = &Entity{File: rel, Name: m[2], Table: pendingTable, Kind: kind}
			pendingTable = ""
			pendingEntity = false
			pendingKey = false
			pendingColumn = ""
			continue
		}
		if cur == nil {
			continue
		}
		if reEntJavaID.MatchString(ln) {
			pendingKey = true
			continue
		}
		if m := reEntJavaColumn.FindStringSubmatch(ln); m != nil {
			pendingColumn = m[1]
			continue
		}
		if m := reEntJavaField.FindStringSubmatch(ln); m != nil {
			mods, typ, name := m[1], strings.TrimSpace(m[2]), m[3]
			if strings.Contains(mods, "static") {
				// static (usually final) fields are constants, not columns.
				pendingKey = false
				pendingColumn = ""
				continue
			}
			if len(cur.Fields) < maxEntityFields {
				cur.Fields = append(cur.Fields, EntityField{
					Name: name, Type: typ, Column: pendingColumn, Key: pendingKey,
				})
			}
			pendingKey = false
			pendingColumn = ""
		}
	}
	flush()
	return out
}

func extractGoEntities(rel, text string) []Entity {
	var out []Entity
	var cur *Entity
	curORM := false
	depth := 0
	flush := func() {
		if cur == nil {
			return
		}
		if len(cur.Fields) > 0 {
			if curORM {
				cur.Kind = "entity"
			}
			out = append(out, *cur)
		}
		cur = nil
		curORM = false
	}
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		if len(out) >= maxEntitiesPerFile {
			cur = nil
			break
		}
		if cur == nil {
			if m := reEntGoStruct.FindStringSubmatch(ln); m != nil {
				cur = &Entity{File: rel, Name: m[1], Kind: "class"}
				depth = 1
				continue
			}
			continue
		}
		depth += strings.Count(ln, "{") - strings.Count(ln, "}")
		if depth <= 0 {
			flush()
			continue
		}
		trim := strings.TrimSpace(ln)
		if trim == "" || strings.HasPrefix(trim, "//") {
			continue
		}
		m := reEntGoField.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		name, typ := m[1], m[2]
		f := EntityField{Name: name, Type: typ}
		if tm := reEntGoTag.FindStringSubmatch(ln); tm != nil {
			tag := tm[1]
			if gm := reTagGorm.FindStringSubmatch(tag); gm != nil {
				curORM = true
				for _, part := range strings.Split(gm[1], ";") {
					part = strings.TrimSpace(part)
					lower := strings.ToLower(part)
					if strings.HasPrefix(lower, "column:") {
						f.Column = strings.TrimSpace(part[len("column:"):])
					}
					if lower == "primarykey" || lower == "primary_key" {
						f.Key = true
					}
				}
			}
			if dm := reTagDB.FindStringSubmatch(tag); dm != nil {
				curORM = true
				if f.Column == "" {
					f.Column = strings.SplitN(dm[1], ",", 2)[0]
				}
			}
			if jm := reTagJSON.FindStringSubmatch(tag); jm != nil && f.Column == "" {
				// json name is a real serialized identifier from code; used only
				// as a display fallback, it does not mark the struct as an entity.
				if v := strings.SplitN(jm[1], ",", 2)[0]; v != "" && v != "-" {
					f.Column = v
				}
			}
		}
		if len(cur.Fields) < maxEntityFields {
			cur.Fields = append(cur.Fields, f)
		}
	}
	flush()
	// Second pass: gorm-style `func (X) TableName() string { return "…" }`
	// (return may sit on the next lines for gofmt-formatted bodies).
	for i, ln := range lines {
		m := reEntGoTableName.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		table := ""
		for j := i; j < len(lines) && j < i+4; j++ {
			if rm := reEntGoReturnStr.FindStringSubmatch(lines[j]); rm != nil {
				table = rm[1]
				break
			}
		}
		if table == "" {
			continue
		}
		for k := range out {
			if out[k].Name == m[1] {
				out[k].Table = table
				out[k].Kind = "entity"
			}
		}
	}
	return out
}

// extractTSEntities is a basic pass over top-level TS interfaces: simple
// `name: type;` members only. TS classes and Python classes are skipped —
// see the Entity doc comment for why.
func extractTSEntities(rel, text string) []Entity {
	var out []Entity
	var cur *Entity
	flush := func() {
		if cur != nil && len(cur.Fields) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, ln := range strings.Split(text, "\n") {
		if len(out) >= maxEntitiesPerFile {
			cur = nil
			break
		}
		if cur == nil {
			if m := reEntTSInterface.FindStringSubmatch(ln); m != nil {
				cur = &Entity{File: rel, Name: m[1], Kind: "interface"}
			}
			continue
		}
		trim := strings.TrimSpace(ln)
		if strings.HasPrefix(trim, "}") {
			flush()
			continue
		}
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") {
			continue
		}
		if strings.ContainsAny(trim, "(){") {
			// method signature or nested object type — not a simple field
			continue
		}
		if m := reEntTSField.FindStringSubmatch(ln); m != nil {
			if len(cur.Fields) < maxEntityFields {
				cur.Fields = append(cur.Fields, EntityField{Name: m[1], Type: strings.TrimSpace(m[2])})
			}
		}
	}
	return out
}
