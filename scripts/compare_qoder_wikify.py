# -*- coding: utf-8 -*-
"""Compare Qoder repowiki samples vs wikify .wikify export shape."""
import collections
import json
import re
from pathlib import Path


def load(p):
    with open(p, encoding="utf-8") as f:
        return json.load(f)


def page_stats(content_dir):
    content_dir = Path(content_dir)
    all_mds = list(content_dir.rglob("*.md"))
    cites = tocs = mermaids = fileuri = line_ranges = related = stubs = 0
    sizes = []
    depths = collections.Counter()
    for p in all_mds:
        rel = p.relative_to(content_dir)
        depths[len(rel.parts)] += 1
        t = p.read_text(encoding="utf-8", errors="ignore")
        sizes.append(len(t))
        if "<cite>" in t:
            cites += 1
        if re.search(r"^##\s*(目录|Table of Contents|TOC)\s*$", t, re.M):
            tocs += 1
        if "```mermaid" in t:
            mermaids += 1
        if "file://" in t:
            fileuri += 1
        if re.search(r"file://[^)\s]+#L\d+", t):
            line_ranges += 1
        if re.search(r"^##\s*(相关页面|Related)", t, re.M):
            related += 1
        if "待补充" in t or "<!-- stub -->" in t or "wikify-stub" in t:
            stubs += 1
    tops = sorted({p.relative_to(content_dir).parts[0] for p in all_mds})
    sizes_sorted = sorted(sizes)
    return {
        "md": len(all_mds),
        "tops": tops,
        "depths": dict(depths),
        "cites": cites,
        "tocs": tocs,
        "mermaids": mermaids,
        "fileuri": fileuri,
        "line_ranges": line_ranges,
        "related": related,
        "stubs": stubs,
        "avg_len": sum(sizes) // max(1, len(sizes)),
        "median_len": sizes_sorted[len(sizes_sorted) // 2] if sizes_sorted else 0,
        "min_len": min(sizes) if sizes else 0,
        "max_len": max(sizes) if sizes else 0,
    }


def meta_stats(meta_path, label):
    m = load(meta_path)
    out = {"label": label, "top_keys": sorted(m.keys())}
    for k in [
        "knowledge_relations",
        "wiki_catalogs",
        "wiki_items",
        "wiki_overview",
        "wiki_readme",
        "wiki_repo",
    ]:
        v = m.get(k)
        if isinstance(v, list):
            out[k] = f"list:{len(v)}"
        elif isinstance(v, dict):
            out[k] = f"dict:{list(v.keys())[:15]}"
        else:
            out[k] = repr(v)[:80]

    items = m.get("wiki_items") or []
    rels = m.get("knowledge_relations") or []
    rel_types = collections.Counter()
    rel_sample = rels[0] if rels else None
    for r in rels:
        if not isinstance(r, dict):
            continue
        rt = (
            r.get("relation_type")
            or r.get("type")
            or r.get("relationType")
            or r.get("relation")
        )
        if not rt:
            rt = "keys:" + ",".join(sorted(r.keys()))
        rel_types[rt if isinstance(rt, str) else str(rt)] += 1
    out["rel_types"] = dict(rel_types)
    out["rel_sample_keys"] = sorted(rel_sample.keys()) if isinstance(rel_sample, dict) else None
    out["rel_sample"] = rel_sample

    dep_n = 0
    track_c = collections.Counter()
    status_c = collections.Counter()
    desc_kebab = 0
    item_keys = set()
    extend_keys = collections.Counter()
    for it in items:
        if not isinstance(it, dict):
            continue
        item_keys |= set(it.keys())
        status_c[it.get("progress_status") or "?"] += 1
        desc = it.get("description") or ""
        if desc and re.match(r"^[a-z0-9]+(?:-[a-z0-9]+)*$", desc):
            desc_kebab += 1
        ext = it.get("extend")
        if isinstance(ext, str) and ext.strip():
            try:
                ej = json.loads(ext)
                for k in ej:
                    extend_keys[k] += 1
                if ej.get("dependent_files"):
                    dep_n += 1
                if ej.get("track"):
                    track_c[ej["track"]] += 1
            except Exception:
                extend_keys["<unparsed>"] += 1
        elif isinstance(ext, dict):
            for k in ext:
                extend_keys[k] += 1
            if ext.get("dependent_files"):
                dep_n += 1
            if ext.get("track"):
                track_c[ext["track"]] += 1
    out["item_keys"] = sorted(item_keys)
    out["extend_keys"] = dict(extend_keys)
    out["dep_n"] = f"{dep_n}/{len(items)}"
    out["tracks"] = dict(track_c)
    out["status"] = dict(status_c)
    out["desc_kebab"] = f"{desc_kebab}/{len(items)}"
    repo = m.get("wiki_repo") or {}
    out["repo_keys"] = sorted(repo.keys()) if isinstance(repo, dict) else None
    ei = repo.get("extend_info") if isinstance(repo, dict) else None
    out["extend_info_keys"] = sorted(ei.keys()) if isinstance(ei, dict) else ei
    return out


def main():
    reports = []
    samples = [
        (
            "Qoder/ILP_NEW",
            r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/zh/content",
            r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/zh/meta/repowiki-metadata.json",
        ),
        (
            "Qoder/scfncb",
            r"E:/Project/NcbProject/scfncb/.qoder/repowiki/zh/content",
            r"E:/Project/NcbProject/scfncb/.qoder/repowiki/zh/meta/repowiki-metadata.json",
        ),
        (
            "Qoder/bte",
            r"E:/Project/NcbProject/bte - 副本/.qoder/repowiki/zh/content",
            r"E:/Project/NcbProject/bte - 副本/.qoder/repowiki/zh/meta/repowiki-metadata.json",
        ),
    ]
    for name, content, meta in samples:
        ps = page_stats(content)
        ms = meta_stats(meta, name)
        reports.append((name, ps, ms))

    w = Path(r"C:/Users/Administrator/AppData/Local/Temp/wikify-e2e-xAp0/.wikify")
    if (w / "content").exists() and (w / "meta/wiki-metadata.json").exists():
        ps = page_stats(w / "content")
        ms = meta_stats(w / "meta/wiki-metadata.json", "wikify/e2e-temp")
        wiki = load(w / "meta/wiki.json")
        tracks = collections.Counter(p.get("track") or "" for p in wiki.get("pages", []))
        dep = sum(1 for p in wiki.get("pages", []) if p.get("dependent_files"))
        ms["wiki_json_tracks"] = dict(tracks)
        ms["wiki_json_dep"] = f"{dep}/{len(wiki.get('pages', []))}"
        # browse
        bi = w / "meta/browse-index.json"
        if bi.exists():
            b = load(bi)
            ms["browse_pages"] = len(b.get("pages") or [])
        reports.append(("wikify/e2e-temp", ps, ms))

    lines = [
        "# wikify vs Qoder 对比报告",
        "",
        "生成时间: 2026-07-23",
        "",
        "> 说明：本机未在 NcbProject 等真实业务仓下找到 `.wikify` 全量产物。",
        "> wikify 侧用 Temp 小样 e2e 导出；Qoder 侧用 NcbProject 真实样例。",
        "> 页数规模不可直接横比，重点比 **形态 / 元数据键 / 页面格式信号**。",
        "",
    ]

    for name, ps, ms in reports:
        lines.append(f"## {name}")
        lines.append("")
        lines.append("### 目录与页面")
        lines.append(f"- md 数: **{ps['md']}**")
        lines.append(f"- 顶层 section: {', '.join(ps['tops'][:25])}")
        lines.append(f"- 路径深度分布: `{ps['depths']}`")
        lines.append(
            f"- 长度 avg/median/min/max: "
            f"{ps['avg_len']}/{ps['median_len']}/{ps['min_len']}/{ps['max_len']}"
        )
        lines.append(
            f"- 格式信号: cite={ps['cites']} toc={ps['tocs']} mermaid={ps['mermaids']} "
            f"file://={ps['fileuri']} #L行号={ps['line_ranges']} "
            f"相关页={ps['related']} stub={ps['stubs']}"
        )
        lines.append("")
        lines.append("### 元数据")
        lines.append(f"- top keys: `{ms['top_keys']}`")
        for k in [
            "knowledge_relations",
            "wiki_catalogs",
            "wiki_items",
            "wiki_overview",
            "wiki_readme",
            "wiki_repo",
        ]:
            lines.append(f"- {k}: {ms.get(k)}")
        lines.append(f"- wiki_item keys: `{ms.get('item_keys')}`")
        lines.append(f"- extend 字段频次: `{ms.get('extend_keys')}`")
        lines.append(f"- dependent_files 覆盖: {ms.get('dep_n')}")
        lines.append(f"- track 分布(extend): `{ms.get('tracks')}`")
        lines.append(f"- progress_status: `{ms.get('status')}`")
        lines.append(f"- description kebab: {ms.get('desc_kebab')}")
        lines.append(f"- relation types: `{ms.get('rel_types')}`")
        lines.append(f"- relation sample keys: `{ms.get('rel_sample_keys')}`")
        if ms.get("wiki_json_tracks") is not None:
            lines.append(
                f"- wiki.json tracks: `{ms['wiki_json_tracks']}` dep={ms['wiki_json_dep']}"
            )
        if ms.get("browse_pages") is not None:
            lines.append(f"- browse-index pages: {ms['browse_pages']}")
        lines.append(f"- wiki_repo keys: `{ms.get('repo_keys')}`")
        lines.append(f"- extend_info: `{ms.get('extend_info_keys')}`")
        lines.append("")

    # page structure samples
    sample_page = Path(
        r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/zh/content/快速开始.md"
    )
    if sample_page.exists():
        t = sample_page.read_text(encoding="utf-8")
        heads = re.findall(r"^#{1,3}\s+.+", t, re.M)[:25]
        lines.append("## Qoder 页面结构样例（ILP 快速开始）")
        lines.append("```")
        lines.extend(heads)
        lines.append("```")
        lines.append("")

    wcontent = Path(r"C:/Users/Administrator/AppData/Local/Temp/wikify-e2e-xAp0/.wikify/content")
    if wcontent.exists():
        for p in sorted(wcontent.rglob("*.md"), key=lambda x: -x.stat().st_size):
            t = p.read_text(encoding="utf-8", errors="ignore")
            heads = re.findall(r"^#{1,3}\s+.+", t, re.M)[:25]
            lines.append(f"## wikify 页面结构样例（{p.name}）")
            lines.append("```")
            lines.extend(heads)
            lines.append("```")
            lines.append("")
            break

    lines.append("## knowledge 目录")
    for label, root in [
        ("Qoder/scfncb", Path(r"E:/Project/NcbProject/scfncb/.qoder/repowiki/knowledge")),
        ("Qoder/ILP_NEW", Path(r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/knowledge")),
        (
            "wikify/e2e",
            Path(r"C:/Users/Administrator/AppData/Local/Temp/wikify-e2e-xAp0/.wikify/knowledge"),
        ),
    ]:
        if root.exists():
            files = [f for f in root.rglob("*") if f.is_file()]
            sample = [str(f.relative_to(root)).replace("\\", "/") for f in files[:8]]
            lines.append(f"- {label}: exists, files={len(files)}, sample={sample}")
        else:
            lines.append(f"- {label}: **missing**")
    lines.append("")

    lines.append("## 路径约定")
    lines.append("| | Qoder | wikify |")
    lines.append("|--|--|--|")
    lines.append("| 根目录 | `.qoder/repowiki/` | `.wikify/` |")
    lines.append("| 正文 | `<lang>/content/**` | `content/**`（lang 记在 `lang` 文件） |")
    lines.append("| 元数据文件名 | `zh/meta/repowiki-metadata.json` | `meta/wiki-metadata.json` |")
    lines.append("| 规划 | 旁路/根 wiki_plan | `.wikify/wiki_plan.yaml` |")
    lines.append("| 浏览索引 | 无独立 browse-index | `meta/browse-index.json` + `wiki.json` |")
    lines.append("")

    lines.append("## 差距结论（形态层）")
    lines.append("")
    lines.append("### 已对齐")
    lines.append("- metadata 六大顶层键：`knowledge_relations` / `wiki_catalogs` / `wiki_items` / `wiki_overview` / `wiki_readme` / `wiki_repo`")
    lines.append("- 页面普遍具备 `<cite>` + `file://` + `## 目录` + mermaid（Qoder 真样例与 wikify 导出均有）")
    lines.append("- 多级 content 目录（section/子页）")
    lines.append("- knowledge 分区 overview / index（wikify 有；Qoder 部分仓有）")
    lines.append("- wiki_items 含 description / progress_status / reference_count / extend")
    lines.append("")
    lines.append("### 部分对齐 / 差异")
    lines.append("- **路径布局**：Qoder 用 `.qoder/repowiki/<lang>/...`；wikify 用 `.wikify/` 单套（有意统一，非 bug）")
    lines.append("- **元数据文件名**：`repowiki-metadata.json` vs `wiki-metadata.json`")
    lines.append("- **track / dependent_files**：Qoder 真样例 extend 多为空 `{}` 或无 track；wikify 在 wiki.json + extend 中显式写 track/dependent_files（wikify 更完整）")
    lines.append("- **relation 字段命名**：需对照 sample 看是否 `relation_type` vs 其它键")
    lines.append("- **wiki_repo 云端字段**：Qoder 有 `optimized_catalog` / `catalogue_think_content` / `recovery_checkpoint` 等；wikify 用 `extend_info.tracks` 等本地扩展")
    lines.append("- **页均长度**：Qoder 真仓 avg ~10k+；小样 e2e 明显更短（规模/LLM 深度差异，非格式键缺失）")
    lines.append("- **业务树深度**：Qoder 真实仓 80–100+ 页、多业务子树；wikify 小样 16 页——需对真实仓全量 generate 才能比业务覆盖")
    lines.append("")
    lines.append("### 建议下一步（若用户提供真实 `.wikify` 路径）")
    lines.append("1. 对同一仓库的 Qoder 产物与 wikify 产物做页标题集合 diff")
    lines.append("2. 抽 5 个业务页比 cite 命中率、mermaid 数量、dependent_files 相关性")
    lines.append("3. 核对 knowledge_relations 的 PARENT_CHILD 是否覆盖全部父子目录")
    lines.append("")

    out_path = Path(r"E:/Project/AI/openread/.compare-qoder-wikify.md")
    out_path.write_text("\n".join(lines), encoding="utf-8")
    print("wrote", out_path)

    # dumps
    print("=== summary ===")
    for name, ps, ms in reports:
        print(
            f"{name}: md={ps['md']} cite={ps['cites']}/{ps['md']} "
            f"toc={ps['tocs']} mermaid={ps['mermaids']} dep={ms.get('dep_n')} "
            f"tracks={ms.get('tracks')} keys={ms.get('top_keys')}"
        )

    print("\n--- ILP relation sample ---")
    print(json.dumps(reports[0][2].get("rel_sample"), ensure_ascii=False, indent=2)[:900])
    print("\n--- wikify relation sample ---")
    if len(reports) > 3:
        print(json.dumps(reports[-1][2].get("rel_sample"), ensure_ascii=False, indent=2)[:900])
    print("\n--- ILP item[0] ---")
    m = load(r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/zh/meta/repowiki-metadata.json")
    print(json.dumps(m["wiki_items"][0], ensure_ascii=False, indent=2)[:1200])
    if (w / "meta/wiki-metadata.json").exists():
        print("\n--- wikify item[0] ---")
        m = load(w / "meta/wiki-metadata.json")
        print(json.dumps(m["wiki_items"][0], ensure_ascii=False, indent=2)[:1400])
        print("\n--- wikify wiki_repo ---")
        print(json.dumps(m.get("wiki_repo"), ensure_ascii=False, indent=2)[:1400])
        print("\n--- ILP wiki_repo keys sample values ---")
        m2 = load(r"E:/Project/NcbProject/ILP_NEW/.qoder/repowiki/zh/meta/repowiki-metadata.json")
        repo = m2.get("wiki_repo") or {}
        slim = {k: (v if not isinstance(v, (dict, list, str)) else (v[:200] if isinstance(v, str) else type(v).__name__ + ":" + str(len(v)))) for k, v in repo.items()}
        print(json.dumps(slim, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
