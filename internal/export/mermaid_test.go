package export

import (
	"strings"
	"testing"

	"github.com/Symon12138/wikify/internal/scan"
)

func erModel() *scan.Model {
	return &scan.Model{
		Name: "demo",
		Entities: []scan.Entity{
			{
				File: "po/UserPO.java", Name: "UserPO", Table: "t_user", Kind: "entity",
				Fields: []scan.EntityField{
					{Name: "id", Type: "Long", Key: true},
					{Name: "userName", Type: "String", Column: "user_name"},
					{Name: "extra", Type: "Map<String, Object>"},
				},
			},
			{
				File: "po/OrderPO.java", Name: "OrderPO", Table: "t-order", Kind: "entity",
				Fields: []scan.EntityField{
					{Name: "id", Type: "Long", Key: true},
					{Name: "userPOId", Type: "Long"},
				},
			},
			{
				File: "svc/OrderService.java", Name: "OrderService", Kind: "class",
				Fields: []scan.EntityField{{Name: "orderDao", Type: "OrderDao"}},
			},
		},
	}
}

func TestEntityERMermaid(t *testing.T) {
	m := erModel()
	out := entityERMermaid(m, []string{"po/UserPO.java", "po/OrderPO.java", "svc/OrderService.java"})
	if out == "" {
		t.Fatal("expected an erDiagram")
	}
	if !strings.Contains(out, "erDiagram") {
		t.Fatalf("missing erDiagram header:\n%s", out)
	}
	if !strings.Contains(out, "t_user {") {
		t.Fatalf("table name from @TableName missing:\n%s", out)
	}
	// Dash in table name must be sanitized to a legal identifier.
	if strings.Contains(out, "t-order") || !strings.Contains(out, "t_order {") {
		t.Fatalf("special chars in table name not sanitized:\n%s", out)
	}
	if !strings.Contains(out, "Long id PK") {
		t.Fatalf("PK marker missing:\n%s", out)
	}
	if !strings.Contains(out, "String user_name") {
		t.Fatalf("column name mapping missing:\n%s", out)
	}
	// erDiagram column types must be single tokens without spaces/angle brackets.
	if strings.Contains(out, "<") || strings.Contains(out, "Map<") {
		t.Fatalf("generic type leaked into erDiagram:\n%s", out)
	}
	if !strings.Contains(out, "Map extra") {
		t.Fatalf("generic type not compacted to base token:\n%s", out)
	}
	// OrderPO.userPOId references UserPO → relation line.
	if !strings.Contains(out, "t_user ||--o{ t_order : userPOId") {
		t.Fatalf("fk-style relation missing:\n%s", out)
	}
	// Kind=class must not appear in the ER diagram.
	if strings.Contains(out, "OrderService") {
		t.Fatalf("non-entity leaked into erDiagram:\n%s", out)
	}
}

func TestEntityERMermaidNoFocusHit(t *testing.T) {
	m := erModel()
	if out := entityERMermaid(m, []string{"web/index.html"}); out != "" {
		t.Fatalf("expected empty output without focus hits, got:\n%s", out)
	}
	if out := entityERMermaid(m, nil); out != "" {
		t.Fatalf("expected empty output for empty focus, got:\n%s", out)
	}
}

func TestClassDiagramMermaid(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Entities: []scan.Entity{
			{
				File: "svc/OrderService.java", Name: "OrderService", Kind: "class",
				Fields: []scan.EntityField{
					{Name: "orderDao", Type: "OrderDao"},
					{Name: "cache", Type: "Map<String, Object>"},
				},
			},
			{
				File: "dao/OrderDao.java", Name: "OrderDao", Kind: "interface",
				Fields: []scan.EntityField{{Name: "ds", Type: "DataSource"}},
			},
			// Two classes in one file → fields only, no method attribution.
			{File: "util/Pair.java", Name: "PairA", Kind: "class",
				Fields: []scan.EntityField{{Name: "left", Type: "String"}}},
			{File: "util/Pair.java", Name: "PairB", Kind: "class",
				Fields: []scan.EntityField{{Name: "right", Type: "String"}}},
		},
		Symbols: map[string][]scan.Symbol{
			"svc/OrderService.java": {
				{Name: "OrderService", Kind: "class", Line: 1},
				{Name: "createOrder", Kind: "method", Line: 10},
				{Name: "cancelOrder", Kind: "method", Line: 20},
			},
			"util/Pair.java": {
				{Name: "swap", Kind: "method", Line: 5},
			},
		},
	}
	out := classDiagramMermaid(m, []string{"svc/OrderService.java", "dao/OrderDao.java", "util/Pair.java"})
	if out == "" {
		t.Fatal("expected a classDiagram")
	}
	if !strings.Contains(out, "classDiagram") {
		t.Fatalf("missing classDiagram header:\n%s", out)
	}
	if !strings.Contains(out, "class OrderService {") {
		t.Fatalf("class missing:\n%s", out)
	}
	if !strings.Contains(out, "+createOrder()") || !strings.Contains(out, "+cancelOrder()") {
		t.Fatalf("single-class file methods missing:\n%s", out)
	}
	if !strings.Contains(out, "<<interface>>") {
		t.Fatalf("interface stereotype missing:\n%s", out)
	}
	// Generic type must use mermaid tilde syntax, never raw angle brackets.
	if strings.Contains(out, "Map<") || !strings.Contains(out, "Map~String,Object~ cache") {
		t.Fatalf("generic type not converted to tilde syntax:\n%s", out)
	}
	// Field-typed association between picked classes.
	if !strings.Contains(out, "OrderService --> OrderDao : orderDao") {
		t.Fatalf("declared-type association missing:\n%s", out)
	}
	// Multi-class file: fields listed, methods not attributed.
	if !strings.Contains(out, "class PairA {") || strings.Contains(out, "+swap()") {
		t.Fatalf("multi-class file must not receive methods:\n%s", out)
	}
}

func TestClassDiagramMermaidNoFocusHit(t *testing.T) {
	m := erModel()
	if out := classDiagramMermaid(m, []string{"other/Nothing.java"}); out != "" {
		t.Fatalf("expected empty output, got:\n%s", out)
	}
}

func TestRouteSequenceMermaid(t *testing.T) {
	m := &scan.Model{
		Name: "demo",
		Routes: []scan.Route{
			{Method: "GET", Path: "/api/orders/{id}", File: "web/OrderController.java", Hint: "spring"},
			{Method: "POST", Path: "/api/orders", File: "web/OrderController.java", Hint: "spring"},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "web/OrderController.java", To: "svc/OrderService.java", Kind: "import"},
			{From: "web/OrderController.java", To: "web/Base.java", Kind: "same_package"},
		},
	}
	out := routeSequenceMermaid(m, []string{"web/OrderController.java", "svc/OrderService.java"})
	if out == "" {
		t.Fatal("expected a sequenceDiagram")
	}
	if !strings.Contains(out, "sequenceDiagram") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "GET /api/orders/{id}") || !strings.Contains(out, "POST /api/orders") {
		t.Fatalf("real route labels missing:\n%s", out)
	}
	if !strings.Contains(out, "OrderController.java") || !strings.Contains(out, "OrderService.java") {
		t.Fatalf("participants missing:\n%s", out)
	}
}

func TestRouteSequenceMermaidNoServiceNeighbor(t *testing.T) {
	m := &scan.Model{
		Routes: []scan.Route{
			{Method: "GET", Path: "/x", File: "web/Handler.java"},
		},
		ImportEdges: []scan.ImportEdge{
			{From: "web/Handler.java", To: "web/Util.java", Kind: "import"},
		},
	}
	if out := routeSequenceMermaid(m, []string{"web/Handler.java"}); out != "" {
		t.Fatalf("must not draw a call chain without a service-like import target:\n%s", out)
	}
}

func TestDefaultMermaidIncludesEntityDiagrams(t *testing.T) {
	m := erModel()
	m.Symbols = map[string][]scan.Symbol{
		"svc/OrderService.java": {{Name: "createOrder", Kind: "method", Line: 3}},
	}
	focus := []string{"po/UserPO.java", "po/OrderPO.java", "svc/OrderService.java"}
	out := defaultMermaid(m, "数据模型", 3, 0, focus...)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "erDiagram") {
		t.Fatalf("expected erDiagram in candidate pool output:\n%s", joined)
	}
	if !strings.Contains(joined, "classDiagram") {
		t.Fatalf("expected classDiagram in candidate pool output:\n%s", joined)
	}
	if !strings.Contains(joined, "t_user") || !strings.Contains(joined, "OrderService") {
		t.Fatalf("expected real extracted names:\n%s", joined)
	}
}
