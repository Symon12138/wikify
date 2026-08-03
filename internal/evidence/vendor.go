// Vendor / third-party library detection over the scan model.
//
// Legacy repositories (especially Java webapps) often commit whole front-end
// library trees (jquery, crypto-js, swiper, ueditor, …) next to business code.
// Minified assets (*.min.js) are already dropped at scan time, but the
// non-minified library sources survive and — without this detector — become
// wiki page candidates ("Encutf16管理", "Swiperesm管理") and crowd evidence
// lists. This file provides a reusable, project-agnostic judgement combining:
//
//   - generic ecosystem knowledge: well-known OSS library names (KnownVendorLibs)
//   - distribution-file naming patterns (*.esm.js, *.umd.js, jquery-3.6.0.js …)
//   - import-graph isolation (whole-library drops have no code relations)
//   - static-asset directory hints (static/, libs/, plugins/, webjars/ …)
//
// Signals are weighted and combined so a single weak signal never kills
// business code; see VendorDetector.Likeness for the exact composition.
//
// The detector lives in package evidence so both evidence and planner can use
// it: evidence must not import planner, while planner → evidence is cycle-free
// (evidence depends only on internal/scan).
package evidence

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Symon12138/wikify/internal/scan"
)

// KnownVendorLibs lists well-known open-source front-end / utility library
// names as they typically appear as directory or file-stem names when a whole
// library is committed into a repository. This is generic ecosystem knowledge
// (never project-private names); extend freely. All entries are lowercase.
var KnownVendorLibs = map[string]bool{
	// DOM / foundation frameworks and runtimes
	"jquery": true, "jquery-ui": true, "jqueryui": true, "jquery-easyui": true,
	"zepto": true, "prototype": true, "mootools": true, "backbone": true,
	"underscore": true, "lodash": true, "ramda": true,
	"angular": true, "angularjs": true, "react": true, "react-dom": true,
	"preact": true, "inferno": true, "vue": true, "vuex": true, "vue-router": true,
	"pinia": true, "svelte": true, "ember": true, "knockout": true,
	"alpinejs": true, "stimulus": true, "htmx": true, "mithril": true,
	"riot": true, "avalon": true, "rxjs": true, "redux": true, "mobx": true,
	"immer": true, "immutable": true,
	"requirejs": true, "seajs": true, "systemjs": true,
	"core-js": true, "corejs": true, "regenerator-runtime": true,
	"babel-polyfill": true, "polyfill": true, "es5-shim": true, "es6-shim": true,
	"html5shiv": true, "respond": true, "modernizr": true,
	// UI kits / component libraries
	"bootstrap": true, "foundation": true, "semantic-ui": true, "bulma": true,
	"materialize": true, "layui": true, "laydate": true, "easyui": true,
	"jeasyui": true, "extjs": true, "element-ui": true, "elementui": true,
	"iview": true, "vant": true, "mint-ui": true, "antd": true,
	"ant-design": true, "tailwindcss": true, "normalize": true, "animate": true,
	"fontawesome": true, "font-awesome": true, "iconfont": true, "ionicons": true,
	// Charts / visualization / maps
	"echarts": true, "zrender": true, "highcharts": true, "chartjs": true,
	"chart.js": true, "d3": true, "three": true, "threejs": true, "plotly": true,
	"apexcharts": true, "antv": true, "mermaid": true, "cytoscape": true,
	"sigma": true, "jointjs": true, "gojs": true,
	"leaflet": true, "openlayers": true, "mapbox": true, "cesium": true,
	"turf": true, "proj4": true,
	// Widgets / effects
	"swiper": true, "slick": true, "owl-carousel": true, "owl.carousel": true,
	"fullpage": true, "iscroll": true, "better-scroll": true,
	"hammer": true, "hammerjs": true, "fastclick": true,
	"sortable": true, "sortablejs": true, "draggable": true, "interact": true,
	"masonry": true, "isotope": true, "lazyload": true, "lazysizes": true,
	"waypoints": true, "scrollreveal": true, "nprogress": true, "pace": true,
	"sweetalert": true, "sweetalert2": true, "toastr": true, "izitoast": true,
	"notyf": true, "select2": true, "chosen": true, "selectize": true,
	"bootstrap-select": true, "bootstrap-table": true,
	"bootstrap-datepicker": true, "bootstrap-datetimepicker": true,
	"daterangepicker": true, "my97datepicker": true, "flatpickr": true,
	"pikaday": true, "fullcalendar": true, "datatables": true,
	"handsontable": true, "ag-grid": true, "tabulator": true,
	"dropzone": true, "plupload": true, "webuploader": true, "uppy": true,
	"cropper": true, "cropperjs": true, "viewerjs": true, "photoswipe": true,
	"fancybox": true, "lightbox": true, "magnific-popup": true,
	// Editors / documents / rendering
	"ueditor": true, "tinymce": true, "ckeditor": true, "quill": true,
	"wangeditor": true, "kindeditor": true, "simditor": true, "summernote": true,
	"codemirror": true, "monaco-editor": true, "katex": true, "mathjax": true,
	"marked": true, "showdown": true, "markdown-it": true,
	"highlightjs": true, "highlight.js": true, "prismjs": true,
	"pdfjs": true, "pdf.js": true, "jspdf": true, "pdfmake": true,
	"html2canvas": true, "xlsx": true, "sheetjs": true, "exceljs": true,
	"papaparse": true, "jszip": true, "pako": true, "filesaver": true,
	"file-saver": true, "qrcode": true, "qrcodejs": true, "jsbarcode": true,
	// Media / games
	"videojs": true, "video.js": true, "plyr": true, "hlsjs": true,
	"hls.js": true, "flvjs": true, "flv.js": true, "ckplayer": true,
	"dplayer": true, "aplayer": true, "howler": true, "wavesurfer": true,
	"pixi": true, "pixijs": true, "phaser": true,
	// Network / util / crypto
	"axios": true, "superagent": true, "sockjs": true, "socket.io": true,
	"stompjs": true, "mqtt": true,
	"moment": true, "dayjs": true, "date-fns": true, "luxon": true,
	"numeral": true, "bignumber": true, "decimal.js": true,
	"crypto-js": true, "cryptojs": true, "jsencrypt": true, "forge": true,
	"sjcl": true, "spark-md5": true, "uuid": true, "nanoid": true,
	"jquery-validation": true, "parsley": true, "dompurify": true,
	"js-cookie": true, "localforage": true, "dexie": true,
	"vconsole": true, "eruda": true,
	// Template engines
	"handlebars": true, "mustache": true, "ejs": true, "nunjucks": true,
	"juicer": true, "jsrender": true, "art-template": true,
}

// vendorAssetDirs are directory names that typically hold static assets /
// dropped-in libraries. A hit is only a weak hint (business JS may live under
// static/ too); paths already excluded by scan.IsNoisePath (node_modules,
// vendor, dist, …) never reach this code.
var vendorAssetDirs = map[string]bool{
	"static": true, "statics": true, "asset": true, "assets": true,
	"lib": true, "libs": true, "library": true, "libraries": true,
	"plugin": true, "plugins": true, "webjars": true, "bower_components": true,
	"thirdparty": true, "third-party": true, "third_party": true,
	"3rdparty": true, "external": true, "externals": true, "public": true,
}

// Signal weights and combination rules (score = sum, clamped to [0,1]):
//
//	known-lib name (dir segment or file stem)  +0.90  — vendor on its own
//	  … but linked outside the library subtree −0.35  — keeps src/react/ apps
//	known-lib prefix of a dashed stem          +0.45  — bootstrap-datepicker.js
//	distribution-file naming pattern           +0.45  — *.esm.js, jquery-3.6.0.js
//	isolated import cluster (≥5 peers)         +0.45  — whole-library drop shape
//	static-asset directory segment             +0.25  — static/, libs/, plugins/
//
// A path is vendor when the total reaches VendorThreshold, i.e. one strong
// signal, or any two of {prefix, pattern, isolation}, or a medium signal plus
// the asset-dir hint. No single weak/medium signal can exceed the threshold,
// so business JS (import-linked, or in business-named directories) survives.
const (
	// VendorThreshold is the Likeness score at or above which a path is
	// treated as committed third-party library material.
	VendorThreshold = 0.6

	vendorKnownLibWeight     = 0.90
	vendorWeakLibWeight      = 0.45
	vendorFilePatternWeight  = 0.45
	vendorIsolatedWeight     = 0.45
	vendorAssetDirWeight     = 0.25
	vendorLinkageDiscount    = 0.35
	vendorIsolatedClusterMin = 5
)

var (
	// Distribution build outputs: swiper.esm.js, foo.umd.min.js, bar.bundle.js …
	reVendorDistName = regexp.MustCompile(`(?i)\.(esm|umd|amd|cjs|iife|bundle|pack|slim|global|min)(\.[a-z0-9_-]+)*\.(m?js|css)$`)
	// Legacy / polyfill split builds: rabbit-legacy.js, index-polyfills.js.
	reVendorLegacyName = regexp.MustCompile(`(?i)-(legacy|polyfills?)\.m?js$`)
	// Version-stamped assets: jquery-3.6.0.js, swiper_4.5.1.min.css.
	reVendorVersioned = regexp.MustCompile(`(?i)[-._]v?\d+(\.\d+){1,3}(-[a-z0-9]+)?(\.(min|slim|bundle|umd|esm))*\.(m?js|css)$`)
	// Version suffix on a name: "jquery-3.6.0" → "jquery", "swiper_v4" → "swiper".
	reVersionSuffix = regexp.MustCompile(`(?i)[-_.]v?\d+(\.\d+)*([-.][0-9a-z]+)*$`)
	// Project-level config stems that must not count as lib hits even when the
	// head token is a framework name: vue.config.js, react.test.js, swiper.d.ts.
	reProjectConfigRest = regexp.MustCompile(`(?i)^(config|conf|settings|setup|test|spec|mock|stories|d)(\..*)?$`)
)

// VendorDetector caches import-graph facts of one scan.Model so repeated
// per-file vendor judgements stay O(1). Zero value / nil are safe (structural
// signals only). scopeInclude patterns (wiki_plan.yaml scope.include) that
// explicitly name a library or asset location exempt matching paths.
type VendorDetector struct {
	include []string
	// neighbors: import-kind adjacency (both directions), ToSlash paths.
	neighbors map[string][]string
	// isolatedInDir: dir+"\x00"+ext → count of code files with zero import edges.
	isolatedInDir map[string]int
	// hasImportEdges: the model carries at least one real import edge; without
	// a graph, "isolation" is meaningless and that signal is disabled.
	hasImportEdges bool
}

// NewVendorDetector indexes model for vendor judgement. scopeInclude may be
// nil; when given, paths matched by an include pattern that itself names a
// known library (e.g. "webapp/js/crypto-js/**") are exempt — broad patterns
// like "src/**" deliberately do NOT disable detection.
func NewVendorDetector(model *scan.Model, scopeInclude []string) *VendorDetector {
	d := &VendorDetector{}
	if len(scopeInclude) > 0 {
		d.include = append([]string(nil), scopeInclude...)
	}
	if model == nil {
		return d
	}
	d.neighbors = map[string][]string{}
	for _, e := range model.ImportEdges {
		// Only true import edges count; "same_package" is co-location, not use.
		if e.Kind != "" && e.Kind != "import" {
			continue
		}
		from, to := filepath.ToSlash(e.From), filepath.ToSlash(e.To)
		if from == "" || to == "" || from == to {
			continue
		}
		d.neighbors[from] = append(d.neighbors[from], to)
		d.neighbors[to] = append(d.neighbors[to], from)
		d.hasImportEdges = true
	}
	d.isolatedInDir = map[string]int{}
	for _, f := range model.Files {
		rel := filepath.ToSlash(f.RelativePath)
		if !scan.IsCodeFile(rel) || len(d.neighbors[rel]) > 0 {
			continue
		}
		d.isolatedInDir[vendorClusterKey(rel)]++
	}
	return d
}

// Likeness returns the vendor score of rel in [0,1]; see the weight table
// above. 0 means "no vendor evidence" (including scope-include exemption).
func (d *VendorDetector) Likeness(rel string) float64 {
	if d == nil {
		d = &VendorDetector{}
	}
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return 0
	}
	if scopeExplicitlyIncludesVendor(rel, d.include) {
		return 0
	}
	segs := strings.Split(rel, "/")
	dirSegs := segs[:len(segs)-1]
	base := segs[len(segs)-1]
	stem := strings.TrimSuffix(base, filepath.Ext(base))

	score := 0.0

	// Signal 1: known library name as a directory segment or as the file stem.
	strong := false
	libRoot := ""
	for i, seg := range dirSegs {
		if vendorLibSegment(seg) {
			strong = true
			libRoot = strings.Join(segs[:i+1], "/")
			break
		}
	}
	if !strong && vendorLibStem(stem) {
		strong = true
		libRoot = vendorDirOf(rel)
	}
	if strong {
		score += vendorKnownLibWeight
		// A file wired into the surrounding codebase (import edges leaving the
		// library subtree) is likely application code in a framework-named
		// directory (src/react/App.jsx) — discount below the threshold.
		if d.linkedOutside(rel, libRoot) {
			score -= vendorLinkageDiscount
		}
	} else if vendorLibStemPrefix(stem) {
		score += vendorWeakLibWeight
	}

	// Signal 2: distribution-file naming pattern.
	if reVendorDistName.MatchString(base) || reVendorLegacyName.MatchString(base) || reVendorVersioned.MatchString(base) {
		score += vendorFilePatternWeight
	}

	// Signal 3: isolated import cluster — the file has zero import relations
	// with the rest of the repository and ≥N same-extension files in the same
	// directory are equally isolated (the shape of a whole-library drop).
	if d.hasImportEdges && scan.IsCodeFile(rel) && len(d.neighbors[rel]) == 0 &&
		d.isolatedInDir[vendorClusterKey(rel)] >= vendorIsolatedClusterMin {
		score += vendorIsolatedWeight
	}

	// Signal 4: static-asset directory hint.
	for _, seg := range dirSegs {
		if vendorAssetDirs[strings.ToLower(seg)] {
			score += vendorAssetDirWeight
			break
		}
	}

	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// IsVendor reports Likeness(rel) >= VendorThreshold.
func (d *VendorDetector) IsVendor(rel string) bool {
	return d.Likeness(rel) >= VendorThreshold
}

// VendorLikeness is the one-shot convenience form of VendorDetector.Likeness
// (no scope exemption). Callers judging many paths should build one detector.
func VendorLikeness(rel string, model *scan.Model) float64 {
	return NewVendorDetector(model, nil).Likeness(rel)
}

// IsKnownVendorLib reports whether name (a directory / module basename) equals
// a well-known library name, version suffix ignored. Exposed so the planner
// can also keep vendor directories out of module-derived page seeds.
func IsKnownVendorLib(name string) bool {
	return vendorLibSegment(name)
}

// linkedOutside reports an import-kind edge between rel and any file outside
// the root subtree (root == "" → never).
func (d *VendorDetector) linkedOutside(rel, root string) bool {
	if d == nil || root == "" {
		return false
	}
	for _, n := range d.neighbors[rel] {
		if n == root || strings.HasPrefix(n, root+"/") {
			continue
		}
		return true
	}
	return false
}

// scopeExplicitlyIncludesVendor reports rel matched by a scope.include pattern
// whose own (wildcard-free) segments name a known library or library file —
// i.e. the user deliberately asked for that material (e.g.
// "webapp/js/crypto-js/**"). Broad catch-alls ("**", "src/**", "static/**")
// never exempt: they express scope, not interest in third-party internals.
func scopeExplicitlyIncludesVendor(rel string, include []string) bool {
	for _, pat := range include {
		p := strings.TrimSpace(pat)
		if p == "" || !scan.MatchPath(p, rel) {
			continue
		}
		for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
			seg = strings.ToLower(strings.TrimSpace(seg))
			if seg == "" || strings.ContainsAny(seg, "*?[") {
				continue
			}
			if vendorLibSegment(seg) {
				return true
			}
			if stem := strings.TrimSuffix(seg, filepath.Ext(seg)); stem != seg && vendorLibStem(stem) {
				return true
			}
		}
	}
	return false
}

// vendorLibSegment: directory segment equals a known library name (exact or
// after stripping a version suffix: "jquery-3.6.0" → "jquery").
func vendorLibSegment(seg string) bool {
	seg = strings.ToLower(strings.TrimSpace(seg))
	if seg == "" {
		return false
	}
	if KnownVendorLibs[seg] {
		return true
	}
	if v := stripVersionSuffix(seg); v != seg && KnownVendorLibs[v] {
		return true
	}
	return false
}

// vendorLibStem: file stem equals a known library name — exact, version
// stripped, or the token before the first dot ("swiper.esm" → "swiper",
// "jquery.cookie" → "jquery"), excluding project-config shapes (vue.config).
func vendorLibStem(stem string) bool {
	s := strings.ToLower(strings.TrimSpace(stem))
	if s == "" {
		return false
	}
	if KnownVendorLibs[s] {
		return true
	}
	if v := stripVersionSuffix(s); v != s && KnownVendorLibs[v] {
		return true
	}
	if i := strings.IndexByte(s, '.'); i > 0 {
		head, rest := s[:i], s[i+1:]
		if !reProjectConfigRest.MatchString(rest) &&
			(KnownVendorLibs[head] || KnownVendorLibs[stripVersionSuffix(head)]) {
			return true
		}
	}
	return false
}

// vendorLibStemPrefix: dashed/underscored stem whose first token is a known
// library ("bootstrap-datepicker" → bootstrap). Weak — plugins usually ride
// with other signals (asset dir / isolation / dist naming).
func vendorLibStemPrefix(stem string) bool {
	s := strings.ToLower(stripVersionSuffix(strings.TrimSpace(stem)))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		return KnownVendorLibs[s[:i]]
	}
	return false
}

func stripVersionSuffix(s string) string {
	return reVersionSuffix.ReplaceAllString(s, "")
}

func vendorDirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return "."
}

func vendorClusterKey(rel string) string {
	return vendorDirOf(rel) + "\x00" + strings.ToLower(filepath.Ext(rel))
}
