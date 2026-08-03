// Package evidence binds wiki pages to dependent source files.
package evidence

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JSHurt/wikify/internal/scan"
)

// PickDependentFiles returns the top scored evidence paths for a page title/goal.
// It avoids "universal" hits (pom.xml, shared constants) when better matches exist,
// diversifies by package segment, and only falls back to generic code when scores are weak.
// Domain matching is driven by title/goal tokens + path stems — no hard-coded product lexicon.
// When model.Root has .wikify/meta/title_bridge.json (from a prior export), path tokens
// and previous page bindings are folded in as extra synonyms (still product-agnostic).
func PickDependentFiles(model *scan.Model, title, goal string, limit int) []string {
	if model == nil || limit <= 0 {
		return nil
	}
	text := title + " " + goal
	tokens := tokenize(text)
	synonyms := expandSynonyms(tokens, text)
	// Optional generated title bridge (prior export artifact).
	if model.Root != "" {
		if br := LoadTitleBridge(DefaultTitleBridgePath(model.Root)); br != nil {
			synonyms = append(synonyms, ExtraSynonymsFromBridge(br, title, goal)...)
			// Prefer prior exact-title paths as soft seeds (still scored below).
			if prior := PathsForTitleHints(br, title); len(prior) > 0 {
				// inject path basenames as tokens so scoring lifts those stems
				for _, p := range prior {
					base := filepath.Base(filepath.ToSlash(p))
					stem := strings.TrimSuffix(base, filepath.Ext(base))
					for _, pt := range splitIdent(stem) {
						synonyms = append(synonyms, strings.ToLower(pt))
					}
				}
			}
		}
	}
	all := unique(append(tokens, synonyms...))
	genericPage := isGenericPage(text)
	// Vendor / third-party library demotion: strong by default, soft when the
	// page is explicitly about such components (前端加密组件, 插件, vendor …).
	vd := NewVendorDetector(model, nil)
	vendorTopic := isVendorTopic(text)

	// Lowercased token set + route ownership index, computed once for the
	// content-level relevance boosts (symbols / routes / config keys).
	lowTokens := make([]string, 0, len(all))
	for _, t := range all {
		t = strings.ToLower(t)
		if len(t) >= 2 {
			lowTokens = append(lowTokens, t)
		}
	}
	routePathsByFile := map[string][]string{}
	for _, rt := range model.Routes {
		f := filepath.ToSlash(rt.File)
		routePathsByFile[f] = append(routePathsByFile[f], strings.ToLower(rt.Path))
	}

	type scored struct {
		path  string
		score float64
		seg   string
	}
	var list []scored
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		lower := strings.ToLower(rel)
		base := filepath.Base(rel)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		camel := splitIdent(stem)
		pathParts := regexp.MustCompile(`[\/_\.\-]+`).Split(rel, -1)
		hay := strings.ToLower(lower + " " + strings.Join(camel, " ") + " " + strings.Join(pathParts, " "))

		var score float64
		for _, tok := range all {
			t := strings.ToLower(tok)
			if len(t) < 2 {
				continue
			}
			if strings.Contains(lower, t) {
				score += 4
			}
			if strings.EqualFold(stem, t) || strings.Contains(strings.ToLower(stem), t) {
				score += 6
			}
			for _, part := range camel {
				pl := strings.ToLower(part)
				if pl == t {
					score += 5
				} else if strings.Contains(pl, t) || strings.Contains(t, pl) {
					score += 3
				}
			}
			for _, part := range pathParts {
				if strings.EqualFold(part, t) {
					score += 4
				}
			}
			if strings.Contains(hay, t) {
				score += 0.5
			}
		}

		// Content-level relevance from scan enrichment — higher precision than
		// path-substring hits (scan-symbols / scan-routes / scan-config-keys).
		if len(model.Symbols) > 0 {
			if syms := model.Symbols[rel]; len(syms) > 0 && symbolNameMatch(syms, lowTokens) {
				score += 5
			}
		}
		if rps := routePathsByFile[rel]; len(rps) > 0 && routePathMatch(rps, lowTokens) {
			score += 4
		}
		if len(model.ConfigKeys) > 0 {
			if keys := model.ConfigKeys[rel]; len(keys) > 0 && configKeyMatch(keys, lowTokens) {
				score += 4
			}
		}

		if matchAny(text, `api|接口|rest|controller|handler|resource|route|控制器|接口索引|控制器索引`) &&
			matchAny(lower, `api|controller|control|handler|resource|route|action|servlet|rest`) {
			score += 4
		}
		if matchAny(text, `草稿|附件|编辑记录|文档|帮助|ueditor|attachment|draft|document`) &&
			matchAny(lower, `draft|attach|upload|file|doc|help|ueditor|editor|attachment|document`) {
			score += 4
		}
		bizLike := !genericPage && matchAny(text, `管理|流程|业务|能力|受理|处理|服务|service|process|capability|workflow`)
		apiLike := matchAny(text, `api|接口|controller|handler|resource|route`)
		if (bizLike || matchAny(text, `服务|service`)) && matchAny(lower, `service|serviceimpl|/svc/|manager`) {
			score += 5
			if matchAny(stem, `(?i)serviceimpl$`) || matchAny(lower, `/service/|/serviceimpl/`) {
				score += 3
			}
		}
		if bizLike && matchAny(lower, `controller|control`) && !apiLike {
			score += 1
		}
		if matchAny(text, `配置|config|yaml|properties`) && matchAny(lower, `\.(ya?ml|properties|xml|toml|ini|conf)$|config|bootstrap|application`) {
			score += 3
		}
		if matchAny(text, `安全|auth|login|security|权限`) && matchAny(lower, `auth|login|security|filter|interceptor|permission`) {
			score += 3
		}
		if matchAny(text, `数据|实体|表|entity|model|sql|mapper|dao|数据库`) && matchAny(lower, `entity|model|dto|dao|mapper|sql|domain|schema|vo|/po/`) {
			score += 3
		}
		if matchAny(text, `部署|运维|deploy|ops|监控`) && matchAny(lower, `docker|k8s|deploy|script|\.sh$|monitor|ops`) {
			score += 2
		}
		if matchAny(text, `工作流|bpmn|workflow|流程`) && matchAny(lower, `bpmn|workflow|activiti|camunda|flow|bpm`) {
			score += 4
		}
		if matchAny(text, `测试|test|spec`) && matchAny(lower, `test|spec|__tests__`) {
			score += 3
		}
		if matchAny(text, `模块划分|组件交互|分层|架构|architecture|module.?layout|package.?structure|工程结构|多模块`) {
			if matchAny(lower, `application\.|app\.java$|main\.go$|main\.py$|/module/|/modules/`) {
				score += 3
			}
			if matchAny(lower, `pom\.xml$|build\.gradle|package\.json$|go\.mod$`) {
				score += 1.5
			}
			if matchAny(lower, `controller|service|config`) {
				score += 1
			}
		}
		if matchAny(text, `前端|界面|视图|静态资源|主题|样式|extjs|vue|react|angular|webapp|ui|ux`) {
			if matchAny(lower, `webapp|static|assets|public|/view/|/views/|/ui/`) {
				score += 4
			}
			if matchAny(lower, `\.(js|jsx|ts|tsx|css|scss|less|vue|jsp|html)$`) {
				score += 2
			}
		}
		if matchAny(text, `日志|审计|log4j|logback|error|异常|排障|分析路径`) {
			if matchAny(lower, `log|audit|error|exception|slf4j|log4j|logback`) {
				score += 3
			}
		}
		if matchAny(text, `构建|发布|打包|ci|cd|编码规范|代码规范|环境搭建|开发环境|本地运行|getting.?started`) {
			if matchAny(lower, `pom\.xml$|build\.gradle|package\.json$|dockerfile|makefile|\.github|jenkins|gitlab-ci|mvnw|gradlew`) {
				score += 2.5
			}
			if matchAny(lower, `readme|application\.(yml|yaml|properties)$|bootstrap`) {
				score += 2
			}
		}
		if matchAny(text, `常见问题|faq|排障|故障|troubleshooting`) {
			if matchAny(lower, `error|exception|log|readme|faq|application\.(yml|yaml|properties)$|config`) {
				score += 3
			}
		}
		if matchAny(text, `性能|扩展|缓存|限流|并发|performance|scalability|cache`) {
			if matchAny(lower, `cache|redis|pool|async|thread|queue|limiter|metric|monitor`) {
				score += 3
			}
		}
		if matchAny(text, `列表查询|条件构造|查询表单|分页`) {
			if matchAny(lower, `query|list|page|form|criteria|search|filter`) {
				score += 3
			}
		}

		if matchAny(lower, `\.(java|kt|ts|tsx|js|jsx|py|go|cs|sql|bpmn)$`) {
			score += 1.5
		} else if matchAny(lower, `\.(xml|yml|yaml|properties)$`) {
			score += 0.3
		}

		// Penalties: universal / low-signal files
		if matchAny(lower, `(^|/)pom\.xml$|(^|/)build\.gradle|(^|/)package\.json$|(^|/)go\.mod$|(^|/)Cargo\.toml$`) {
			if genericPage {
				score += 0.5 // overview may cite build files lightly
			} else {
				score -= 14 // strong demotion — business pages should not bind to pom
			}
		}
		if !genericPage && matchAny(lower, `(^|/)pom\.xml$`) {
			score -= 4
		}
		// Constants / dict / code-table files rarely explain a capability.
		// Also demote *Const.java / *Constants.java suffixes (not only exact stem match).
		if matchAny(stem, `(?i)^(const|constant|constants|commonconst|appconst|baseconst|sysconst|globalconst|dict|dictconst|codeconst)$`) ||
			matchAny(stem, `(?i).*(const|constants)$`) ||
			matchAny(lower, `/(const|constants|dict)/`) {
			if !matchAny(text, `常量|字典|编码|code.?table|dictionary`) {
				score -= 14
				// Overview/generic pages still should not top-rank pure constants.
				if genericPage {
					score -= 4
				}
			}
		}
		// Static HTML / pure markup is weak evidence except for frontend/UI pages.
		// Extra demote readme.html / button asset placeholders which are universal noise.
		if matchAny(lower, `\.(html|htm|xhtml)$`) {
			if matchAny(lower, `readme\.html$|/button/|/help/.*\.html$`) {
				score -= 22
			} else if matchAny(text, `前端|界面|视图|静态|jsp|html|webapp|ui|ux|主题`) {
				score -= 1
			} else {
				score -= 18
			}
		}
		// CSS / image assets almost never belong on backend capability pages.
		if matchAny(lower, `\.(css|scss|less|png|jpe?g|gif|svg|ico|woff2?|ttf)$`) {
			if !matchAny(text, `前端|界面|样式|主题|静态|ui|ux|css`) {
				score -= 9
			}
		}
		if matchAny(stem, `(?i)^(base|abstract|common|util|utils|helper|dataentity)$`) {
			score -= 3
		}
		// Non-source artifacts should not crowd business pages even when tokens match.
		if !genericPage && !matchAny(lower, `\.(java|kt|ts|tsx|js|jsx|py|go|cs|sql|bpmn|xml|yml|yaml|properties)$`) {
			score -= 6
		}
		// Mild boost for dao/mapper/repo on data-ish or business pages so role chains form.

		if !genericPage && matchAny(lower, `dao|mapper|repository|/repo/`) {
			score += 1.5
		}
		// Prefer real source tree over exploded WEB-INF/classes copies.
		if matchAny(lower, `/web-inf/classes/`) {
			score -= 5
		}
		// Logging scaffolding + rich-text editor configs are universal noise on
		// capability pages (except explicit log/audit/editor topics).
		if matchAny(lower, `log4j|logback`) && !matchAny(text, `日志|审计|log4j|logback|排障`) {
			score -= 16
		}
		if matchAny(lower, `/ueditor/|chart\.config\.js$|mybatis-config\.xml$`) &&
			!matchAny(text, `编辑|附件|ueditor|草稿|帮助文档|document`) {
			score -= 16
		}
		// Committed third-party library files (crypto-js, swiper, jquery plugins…)
		// almost never explain a business capability. Strong demotion, softened
		// when the page is explicitly about front-end components / plugins so
		// pages like「前端加密组件」can still cite them.
		if vd.Likeness(rel) >= VendorThreshold {
			if vendorTopic {
				score -= 4
			} else {
				score -= 16
			}
		}
		// Generic overview pages: demote pure constants/html even harder so they
		// do not monopolise dependent_files across unrelated titles.
		if genericPage {
			if matchAny(stem, `(?i).*(const|constants)$`) || matchAny(lower, `/constant/`) {
				if !matchAny(text, `常量|字典|编码`) {
					score -= 6
				}
			}
			if matchAny(lower, `readme\.html$`) {
				score -= 8
			}
		}

		if matchAny(lower, `readme|\.md$`) {
			score -= 2
		}
		if scan.IsNoisePath(lower) {
			score -= 8
		}

		// Hard floor: universal noise cannot enter the candidate list on generic
		// pages when its score is only carried by path-token accidents.
		if isUniversalNoisePath(rel) && !matchAny(text, `常量|字典|编码|code.?table|dictionary|构建|发布|打包`) {
			if genericPage {
				score -= 20
			} else if score < 12 {
				// business pages: only keep noise if it somehow scored very high (unlikely)
				continue
			}
		}
		if score >= minScore(genericPage) {
			list = append(list, scored{path: f.RelativePath, score: score, seg: packageSeg(rel)})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].path < list[j].path
	})

	if model != nil && len(list) > 0 && (len(model.ImportEdges) > 0 || len(model.EntryPoints) > 0) {
		byPath := map[string]int{}
		for i, item := range list {
			byPath[filepath.ToSlash(item.path)] = i
		}
		seedN := 5
		if seedN > len(list) {
			seedN = len(list)
		}
		for si := 0; si < seedN; si++ {
			seed := filepath.ToSlash(list[si].path)
			for _, nb := range model.ImportNeighbors(seed, 8) {
				p := filepath.ToSlash(nb)
				if idx, ok := byPath[p]; ok {
					list[idx].score += 3.5
				} else if !scan.IsNoisePath(p) && !isUniversalNoisePath(p) && !(vd.IsVendor(p) && !vendorTopic) {
					list = append(list, scored{path: p, score: list[si].score*0.55 + 2, seg: packageSeg(p)})
					byPath[p] = len(list) - 1
				}
			}
			for _, sm := range model.SameModuleFiles(seed, 4) {
				p := filepath.ToSlash(sm)
				if idx, ok := byPath[p]; ok {
					list[idx].score += 1.2
				}
			}
		}
		if genericPage {
			for _, ep := range model.EntryPoints {
				p := filepath.ToSlash(ep.Path)
				if idx, ok := byPath[p]; ok {
					list[idx].score += 1.5
				}
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].score != list[j].score {
				return list[i].score > list[j].score
			}
			return list[i].path < list[j].path
		})
	}

	out := make([]string, 0, limit)
	seen := map[string]bool{}
	segCount := map[string]int{}
	maxPerSeg := 2
	if limit <= 4 {
		maxPerSeg = 3
	}

	pick := func(respectSeg bool) {
		for _, item := range list {
			if len(out) >= limit {
				return
			}
			p := filepath.ToSlash(item.path)
			if seen[p] {
				continue
			}
			// Drop universal noise unless the page is explicitly about constants/dict/build.
			if isUniversalNoisePath(p) && !matchAny(text, `常量|字典|编码|code.?table|dictionary|构建|发布|打包|pom|maven|gradle`) {
				// Always skip for non-generic; for generic only skip when better candidates exist
				// (list has any non-noise) or score is not dominant.
				if !genericPage {
					continue
				}
				hasBetter := false
				for _, o := range list {
					if !isUniversalNoisePath(o.path) {
						hasBetter = true
						break
					}
				}
				if hasBetter {
					continue
				}
			}
			if respectSeg && item.seg != "" && segCount[item.seg] >= maxPerSeg {
				continue
			}
			seen[p] = true
			if item.seg != "" {
				segCount[item.seg]++
			}
			out = append(out, p)
		}
	}
	pick(true)
	if len(out) < limit {
		pick(false)
	}

	if len(out) < limit && model != nil {
		seeds := append([]string{}, out...)
		for _, seed := range seeds {
			if len(out) >= limit {
				break
			}
			for _, nb := range model.ImportNeighbors(seed, 6) {
				p := filepath.ToSlash(nb)
				if seen[p] || scan.IsNoisePath(p) || isUniversalNoisePath(p) || (vd.IsVendor(p) && !vendorTopic) {
					continue
				}
				seen[p] = true
				out = append(out, p)
				if len(out) >= limit {
					break
				}
			}
		}
	}

	if len(out) < min(3, limit) && genericPage {
		for _, fb := range collectRoleFallback(model, text, limit*2, vd) {
			p := filepath.ToSlash(fb)
			if seen[p] || isUniversalNoisePath(p) {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		}
	}
	if len(out) < min(2, limit) && genericPage {
		for _, fb := range collectFallback(model, limit, vd) {
			p := filepath.ToSlash(fb)
			if seen[p] || isUniversalNoisePath(p) {
				continue
			}
			if matchAny(strings.ToLower(p), `pom\.xml$|package\.json$|go\.mod$`) {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// isUniversalNoisePath reports paths that almost never explain a capability
// (shared constants, button/readme HTML placeholders, pure build manifests,
// logging/editor scaffolding, exploded WEB-INF class copies).
func isUniversalNoisePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.HasSuffix(lower, "pom.xml") || strings.HasSuffix(lower, "package.json") ||
		strings.HasSuffix(lower, "go.mod") || strings.HasSuffix(lower, "build.gradle") {
		return true
	}
	if strings.HasSuffix(lower, "readme.html") || strings.Contains(lower, "/button/") && strings.HasSuffix(lower, ".html") {
		return true
	}
	if strings.HasSuffix(stem, "const") || strings.HasSuffix(stem, "constants") {
		return true
	}
	if strings.Contains(lower, "/constant/") || strings.Contains(lower, "/constants/") {
		return true
	}
	// Logging scaffolding rarely explains a business capability.
	if strings.Contains(base, "log4j") || strings.Contains(base, "logback") {
		return true
	}
	// Exploded WEB-INF/classes copies are low-signal duplicates.
	if strings.Contains(lower, "/web-inf/classes/") {
		return true
	}
	// Rich-text editor scaffolding + chart/template configs.
	if strings.Contains(lower, "/ueditor/") {
		return true
	}
	if base == "chart.config.js" || (strings.Contains(lower, "/dialogs/") && strings.HasSuffix(base, "config.js")) {
		return true
	}
	if base == "mybatis-config.xml" || strings.HasSuffix(lower, "/mybatis-config.xml") {
		return true
	}
	return false
}

// isVendorTopic reports pages explicitly about third-party / front-end
// components, where citing committed library files is legitimate.
func isVendorTopic(text string) bool {
	return matchAny(text, `第三方|三方库|前端库|组件库|加密组件|前端组件|静态资源|插件|`+
		`vendor|third.?party|library|plugin|widget`)
}

func minScore(generic bool) float64 {
	if generic {
		return 2.5
	}
	return 5.0
}

func isGenericPage(text string) bool {
	return matchAny(text, `项目概述|快速开始|快速上手|开发指南|目录结构|技术栈|`+
		`overview|getting.?started|developer.?guide|architecture|`+
		`架构设计|整体架构|分层架构|模块划分|组件交互|工程结构|多模块|`+
		`部署与运维|故障排查|配置管理|常见问题|faq|编码规范|代码规范|`+
		`构建与发布|构建脚本|性能与扩展|环境搭建|开发环境|本地运行|`+
		`推荐阅读|阅读路径|适用场景|平台定位|导读|`+
		`日志|审计|排障|troubleshooting|coding.?standard|style.?guide|`+
		`frontend|前端|静态资源|主题风格|`+
		`rest|控制器索引|接口索引|api.?index|controller.?index|`+
		`草稿|附件|编辑记录|帮助文档|document.?store`)
}

func packageSeg(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	noise := map[string]bool{
		"src": true, "main": true, "java": true, "resources": true, "com": true,
		"org": true, "net": true, "cn": true, "impl": true, "controller": true,
		"service": true, "dao": true, "mapper": true, "web": true, "app": true,
	}
	for i := len(parts) - 2; i >= 0; i-- {
		p := strings.ToLower(parts[i])
		if noise[p] || len(p) < 2 {
			continue
		}
		return p
	}
	return strings.ToLower(filepath.Base(rel))
}

func collectFallback(model *scan.Model, limit int, vd *VendorDetector) []string {
	var out []string
	for _, f := range model.Files {
		if scan.IsCodeFile(f.RelativePath) && !scan.IsNoisePath(f.RelativePath) &&
			!isUniversalNoisePath(f.RelativePath) && !vd.IsVendor(f.RelativePath) {
			out = append(out, f.RelativePath)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func collectRoleFallback(model *scan.Model, text string, limit int, vd *VendorDetector) []string {
	if model == nil || limit <= 0 {
		return nil
	}
	type hit struct {
		path  string
		score int
	}
	var hits []hit
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		lower := strings.ToLower(rel)
		if scan.IsNoisePath(lower) || isUniversalNoisePath(rel) {
			continue
		}
		if vd.IsVendor(rel) && !isVendorTopic(text) {
			continue
		}
		s := 0
		switch {
		case matchAny(text, `api|接口|rest|控制器|controller|接口索引|控制器索引`):
			if matchAny(lower, `controller|control|handler|resource|servlet|rest`) {
				s += 6
			}
		case matchAny(text, `草稿|附件|编辑记录|帮助文档|document|attachment|draft`):
			if matchAny(lower, `draft|attach|upload|help|ueditor|editor|document|file`) {
				s += 6
			}
		case matchAny(text, `前端|界面|视图|静态|主题|webapp|ui`):
			if matchAny(lower, `webapp|static|assets|public|\.(js|css|jsp|vue|tsx?|html)$`) {
				s += 6
			}
		case matchAny(text, `日志|审计|error|异常|排障`):
			if matchAny(lower, `log|audit|error|exception|log4j|logback`) {
				s += 6
			}
		case matchAny(text, `配置|环境|搭建|本地运行|getting.?started`):
			if matchAny(lower, `application\.(yml|yaml|properties)$|bootstrap|config|readme`) {
				s += 6
			}
		case matchAny(text, `构建|发布|编码规范|代码规范`):
			if matchAny(lower, `pom\.xml$|build\.gradle|package\.json$|dockerfile|readme|makefile`) {
				s += 5
			}
		case matchAny(text, `性能|扩展|缓存`):
			if matchAny(lower, `cache|redis|pool|async|metric`) {
				s += 6
			}
		case matchAny(text, `模块划分|组件交互|架构|工程结构|多模块`):
			if matchAny(lower, `application|main\.|pom\.xml$|controller|service|config`) {
				s += 4
			}
		case matchAny(text, `常见问题|faq|故障`):
			if matchAny(lower, `error|exception|readme|application\.(yml|yaml|properties)$|log`) {
				s += 5
			}
		default:
			if matchAny(lower, `application\.|main\.go$|readme|application\.(yml|yaml|properties)$`) {
				s += 3
			}
		}
		if s > 0 {
			hits = append(hits, hit{path: f.RelativePath, score: s})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].path < hits[j].path
	})
	var out []string
	for _, h := range hits {
		out = append(out, h.path)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func tokenize(text string) []string {
	text = regexp.MustCompile(`([a-z])([A-Z])`).ReplaceAllString(text, `${1} ${2}`)
	parts := regexp.MustCompile(`[^a-zA-Z0-9\p{Han}]+`).Split(text, -1)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len([]rune(p)) >= 2 {
			out = append(out, p)
		}
		if len(out) >= 24 {
			break
		}
	}
	return out
}

func splitIdent(stem string) []string {
	stem = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(stem, `${1} ${2}`)
	stem = regexp.MustCompile(`[_\-\.]+`).ReplaceAllString(stem, " ")
	parts := strings.Fields(stem)
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandSynonyms(tokens []string, text string) []string {
	type bridge struct {
		re  *regexp.Regexp
		val []string
	}
	bridges := []bridge{
		{regexp.MustCompile(`(?i)接口|api`), []string{"api", "controller", "control", "handler", "resource", "action", "servlet", "endpoint"}},
		{regexp.MustCompile(`(?i)服务|service`), []string{"service", "svc", "manager"}},
		{regexp.MustCompile(`(?i)配置|config`), []string{"config", "properties", "yml", "yaml", "xml", "bootstrap", "application"}},
		{regexp.MustCompile(`(?i)安全|认证|授权|权限`), []string{"auth", "login", "security", "permission", "filter", "interceptor", "token"}},
		{regexp.MustCompile(`(?i)数据|实体|模型|表结构|数据库`), []string{"entity", "model", "dto", "dao", "mapper", "sql", "domain", "schema", "vo", "po"}},
		{regexp.MustCompile(`(?i)部署|运维|监控`), []string{"deploy", "docker", "k8s", "ops", "monitor", "script"}},
		{regexp.MustCompile(`(?i)工作流|流程|bpmn`), []string{"workflow", "bpmn", "activiti", "camunda", "flow", "bpm"}},
		{regexp.MustCompile(`(?i)批处理|批量|定时|任务`), []string{"batch", "job", "schedule", "quartz", "cron", "task"}},
		{regexp.MustCompile(`(?i)实时`), []string{"realtime", "stream", "listener", "event", "mq", "kafka"}},
		{regexp.MustCompile(`(?i)测试`), []string{"test", "spec", "junit", "mock"}},
		{regexp.MustCompile(`(?i)前端|界面|视图|静态资源`), []string{"webapp", "static", "assets", "view", "views", "ui", "jsp", "extjs"}},
		{regexp.MustCompile(`(?i)日志|审计`), []string{"log", "audit", "log4j", "logback", "slf4j"}},
		{regexp.MustCompile(`(?i)构建|发布|打包`), []string{"build", "deploy", "docker", "maven", "gradle", "ci"}},
		{regexp.MustCompile(`(?i)编码规范|代码规范`), []string{"checkstyle", "eslint", "lint", "editorconfig", "style"}},
		{regexp.MustCompile(`(?i)性能|缓存|扩展`), []string{"cache", "redis", "pool", "async", "metric", "monitor"}},
		{regexp.MustCompile(`(?i)模块划分|组件交互|工程结构`), []string{"module", "application", "config", "controller", "service"}},
		{regexp.MustCompile(`(?i)常见问题|faq|排障`), []string{"error", "exception", "readme", "faq", "log"}},
		{regexp.MustCompile(`(?i)支付`), []string{"pay", "payment"}},
		{regexp.MustCompile(`(?i)订单`), []string{"order"}},
		{regexp.MustCompile(`(?i)用户|账户|账号`), []string{"user", "account", "acct"}},
		{regexp.MustCompile(`(?i)客户`), []string{"cust", "customer", "client"}},
		{regexp.MustCompile(`(?i)合同`), []string{"contract"}},
		{regexp.MustCompile(`(?i)结算`), []string{"settle", "settlement"}},
		{regexp.MustCompile(`(?i)风控|风险`), []string{"risk"}},
		{regexp.MustCompile(`(?i)审计`), []string{"audit"}},
		{regexp.MustCompile(`(?i)指标|度量`), []string{"metric", "quota", "indicator", "kpi"}},
	}
	var out []string
	for _, b := range bridges {
		if b.re.MatchString(text) {
			out = append(out, b.val...)
			continue
		}
		for _, tok := range tokens {
			if b.re.MatchString(tok) {
				out = append(out, b.val...)
				break
			}
		}
	}
	for _, tok := range tokens {
		if regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]+$`).MatchString(tok) {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

// symbolNameMatch reports whether any lowercased title/goal token exactly
// equals a declared symbol name (case-insensitive). Exact equality keeps the
// boost high-precision — substring hits are already covered by path scoring.
func symbolNameMatch(syms []scan.Symbol, lowTokens []string) bool {
	for _, s := range syms {
		nl := strings.ToLower(s.Name)
		if nl == "" {
			continue
		}
		for _, t := range lowTokens {
			if nl == t {
				return true
			}
		}
	}
	return false
}

// routePathMatch reports whether any token appears inside a route path owned
// by the candidate file (route paths pre-lowercased by the caller).
func routePathMatch(routePathsLower, lowTokens []string) bool {
	for _, rp := range routePathsLower {
		for _, t := range lowTokens {
			if strings.Contains(rp, t) {
				return true
			}
		}
	}
	return false
}

// configKeyMatch reports whether any token exactly equals a top-level config
// key of the candidate file (case-insensitive).
func configKeyMatch(keys, lowTokens []string) bool {
	for _, k := range keys {
		kl := strings.ToLower(strings.TrimSpace(k))
		if kl == "" {
			continue
		}
		for _, t := range lowTokens {
			if kl == t {
				return true
			}
		}
	}
	return false
}

func matchAny(text, pattern string) bool {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return false
	}
	return re.MatchString(text)
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
