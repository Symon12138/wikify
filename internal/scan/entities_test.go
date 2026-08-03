package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractEntitiesJavaTableName(t *testing.T) {
	src := `package com.example.po;

import java.io.Serializable;

@TableName("t_user")
public class UserPO implements Serializable {
    private static final long serialVersionUID = 1L;

    @TableId
    private Long id;

    @TableField("user_name")
    private String userName;

    private Integer age;

    public Long getId() { return id; }
}
`
	ents := extractEntities(".java", "src/main/java/com/example/po/UserPO.java", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %d: %+v", len(ents), ents)
	}
	e := ents[0]
	if e.Name != "UserPO" || e.Table != "t_user" || e.Kind != "entity" {
		t.Fatalf("unexpected entity header: %+v", e)
	}
	if len(e.Fields) != 3 {
		t.Fatalf("expected 3 fields (serialVersionUID skipped), got %+v", e.Fields)
	}
	if e.Fields[0].Name != "id" || !e.Fields[0].Key || e.Fields[0].Type != "Long" {
		t.Fatalf("id field wrong: %+v", e.Fields[0])
	}
	if e.Fields[1].Name != "userName" || e.Fields[1].Column != "user_name" {
		t.Fatalf("userName field wrong: %+v", e.Fields[1])
	}
	if e.Fields[2].Name != "age" || e.Fields[2].Key || e.Fields[2].Column != "" {
		t.Fatalf("age field wrong: %+v", e.Fields[2])
	}
}

func TestExtractEntitiesJavaJpa(t *testing.T) {
	src := `@Entity
@Table(name = "orders")
public class Order {
    @Id
    @Column(name = "order_id")
    private Long id;
    private java.math.BigDecimal amount;
}
`
	ents := extractEntities(".java", "Order.java", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %+v", ents)
	}
	e := ents[0]
	if e.Kind != "entity" || e.Table != "orders" {
		t.Fatalf("jpa table not picked up: %+v", e)
	}
	if len(e.Fields) != 2 || !e.Fields[0].Key || e.Fields[0].Column != "order_id" {
		t.Fatalf("jpa fields wrong: %+v", e.Fields)
	}
}

func TestExtractEntitiesJavaPlainClass(t *testing.T) {
	src := `public class OrderService {
    private OrderDao orderDao;
    private Map<String, Object> cache = new HashMap<>();

    public void process() {}
}

interface Notifier {
}
`
	ents := extractEntities(".java", "OrderService.java", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity (field-less interface dropped), got %+v", ents)
	}
	e := ents[0]
	if e.Name != "OrderService" || e.Kind != "class" || e.Table != "" {
		t.Fatalf("plain class wrong: %+v", e)
	}
	if len(e.Fields) != 2 || e.Fields[0].Type != "OrderDao" || e.Fields[1].Type != "Map<String, Object>" {
		t.Fatalf("plain class fields wrong: %+v", e.Fields)
	}
}

func TestExtractEntitiesGoStructTags(t *testing.T) {
	src := "package model\n\n" +
		"type User struct {\n" +
		"\tID        int64  `gorm:\"column:id;primaryKey\" json:\"id\"`\n" +
		"\tUserName  string `gorm:\"column:user_name\"`\n" +
		"\tNick      string `db:\"nick_name\"`\n" +
		"\tInternal  string\n" +
		"}\n\n" +
		"func (u *User) TableName() string {\n" +
		"\treturn \"t_users\"\n" +
		"}\n"
	ents := extractEntities(".go", "model/user.go", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %+v", ents)
	}
	e := ents[0]
	if e.Kind != "entity" || e.Table != "t_users" || e.Name != "User" {
		t.Fatalf("go entity header wrong: %+v", e)
	}
	if len(e.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %+v", e.Fields)
	}
	if !e.Fields[0].Key || e.Fields[0].Column != "id" {
		t.Fatalf("gorm primaryKey/column not parsed: %+v", e.Fields[0])
	}
	if e.Fields[1].Column != "user_name" || e.Fields[2].Column != "nick_name" {
		t.Fatalf("gorm/db columns wrong: %+v", e.Fields)
	}
}

func TestExtractEntitiesGoPlainStruct(t *testing.T) {
	src := "package cfg\n\n" +
		"type Options struct {\n" +
		"\tMaxFiles int\n" +
		"\tName     string `json:\"name\"`\n" +
		"}\n"
	ents := extractEntities(".go", "cfg/options.go", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %+v", ents)
	}
	e := ents[0]
	if e.Kind != "class" || e.Table != "" {
		t.Fatalf("plain struct must be Kind=class: %+v", e)
	}
	if len(e.Fields) != 2 || e.Fields[1].Column != "name" {
		t.Fatalf("plain struct fields wrong: %+v", e.Fields)
	}
}

func TestExtractEntitiesTSInterface(t *testing.T) {
	src := `export interface UserDTO {
  id: number;
  name?: string;
  toString(): string;
}
`
	ents := extractEntities(".ts", "types/user.ts", src)
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %+v", ents)
	}
	e := ents[0]
	if e.Kind != "interface" || e.Name != "UserDTO" {
		t.Fatalf("ts interface wrong: %+v", e)
	}
	if len(e.Fields) != 2 || e.Fields[0].Type != "number" || e.Fields[1].Name != "name" {
		t.Fatalf("ts fields wrong (method must be skipped): %+v", e.Fields)
	}
}

func TestExtractEntitiesFieldCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("public class Big {\n")
	for i := 0; i < maxEntityFields+10; i++ {
		sb.WriteString("    private int f")
		sb.WriteString(strings.Repeat("x", i%3+1))
		sb.WriteString(string(rune('a' + i%26)))
		sb.WriteString(";\n")
	}
	sb.WriteString("}\n")
	ents := extractEntities(".java", "Big.java", sb.String())
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(ents))
	}
	if len(ents[0].Fields) > maxEntityFields {
		t.Fatalf("field cap not enforced: %d", len(ents[0].Fields))
	}
}

func TestScanFillsEntities(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "model", "user.go"),
		"package model\n\ntype User struct {\n\tID int64 `gorm:\"column:id;primaryKey\"`\n\tName string `gorm:\"column:name\"`\n}\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entities) != 1 {
		t.Fatalf("expected 1 scanned entity, got %+v", m.Entities)
	}
	e := m.Entities[0]
	if e.File != "model/user.go" || e.Kind != "entity" || e.Name != "User" {
		t.Fatalf("scanned entity wrong: %+v", e)
	}
}

func TestGraphFileRoundTripEntities(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "model", "user.go"),
		"package model\n\ntype User struct {\n\tID int64 `gorm:\"column:id;primaryKey\"`\n}\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entities) == 0 {
		t.Fatal("scan produced no entities")
	}
	path := DefaultGraphPath(dir)
	if err := WriteGraphFile(path, m); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGraphFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Entities) != 1 || g.Entities[0].Name != "User" || !g.Entities[0].Fields[0].Key {
		t.Fatalf("entities did not round-trip: %+v", g.Entities)
	}
	// Fresh scan auto-loads the graph file; fresh extraction wins, so still 1.
	m2, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Entities) != 1 {
		t.Fatalf("entities duplicated or lost on overlay merge: %+v", m2.Entities)
	}
}

func TestGraphFileWithoutEntitiesBackCompat(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	// Old-style graph.json lacking the entities key must load and apply cleanly.
	mustWrite(t, filepath.Join(dir, ".wikify", "graph.json"),
		`{"import_edges":[{"from":"a.go","to":"a.go"}]}`)
	g, err := LoadGraphFile(filepath.Join(dir, ".wikify", "graph.json"))
	if err != nil {
		t.Fatalf("old graph.json must load: %v", err)
	}
	if g.Entities != nil {
		t.Fatalf("missing key must decode to nil, got %+v", g.Entities)
	}
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = m // Scan applying the overlay must not panic or error.
}

func TestApplyGraphFileFillsEntitiesForUncoveredFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "legacy.py"), "class Legacy:\n    pass\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entities) != 0 {
		t.Fatalf("python must not be extracted natively: %+v", m.Entities)
	}
	g := &GraphFile{Entities: []Entity{
		{File: "legacy.py", Name: "Legacy", Kind: "class", Fields: []EntityField{{Name: "x", Type: "int"}}},
		{File: "missing.py", Name: "Ghost", Kind: "class"},
	}}
	ApplyGraphFile(m, g)
	if len(m.Entities) != 1 || m.Entities[0].Name != "Legacy" {
		t.Fatalf("overlay fill wrong (unknown paths must drop): %+v", m.Entities)
	}
	// Ensure round-trip via os file too (writability of merged model).
	if err := os.MkdirAll(filepath.Join(dir, ".wikify"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteGraphFile(DefaultGraphPath(dir), m); err != nil {
		t.Fatal(err)
	}
}
