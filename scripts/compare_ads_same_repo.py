# -*- coding: utf-8 -*-
"""Same-repo compare: ADS com.sunyard.common — Qoder vs wikify (detailed)."""
import collections
import json
import re
from difflib import SequenceMatcher
from pathlib import Path

ROOT = Path(r"F:/ALDrive/9月资源/ADS/com.sunyard.common")
OUT = Path(r"E:/Project/AI/openread/.compare-ads-qoder-wikify.md")


def load(p):
    return json.loads(Path(p).read_text(encoding="utf-8"))


def page_stats(content_dir):
    content_dir = Path(content_dir)
    all_mds = list(content_dir.rglob("*.md"))
    cites = tocs = mermaids = fileuri = line_ranges = related = stubs = 0
    sizes = []
    depths = collections.Counter()
    h2 = []
    cite_n = []
    for p in all_mds:
        depths[len(p.relative_to(content_dir).parts)] += 1
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
        if "待补充" in t or "wikify-stub" in t:
            stubs += 1
        h2.append(len(re.findall(r"^##\s+", t, re.M)))
        cite_n.append(len(set(re.findall(r"file://([^)\s#]+)", t))))
    tops = sorted({p.relative_to(content_dir).parts[0] for p in all_mds})
    n = max(1, len(all_mds))
    ss = sorted(sizes)
    return dict(
        md=len(all_mds),
        tops=tops,
        depths=dict(depths),
        cites=cites,
        tocs=tocs,
        mermaids=mermaids,
        fileuri=fileuri,
        line_ranges=line_ranges,
        related=related,
        stubs=stubs,
        avg_len=sum(sizes) // n,
        median_len=ss[len(ss) // 2],
        min_len=min(sizes),
        max_len=max(sizes),
        avg_h2=sum(h2) / n,
        avg_cite=sum(cite_n) / n,
    )


def titles_from_meta(meta_path):
    m = load(meta_path)
    items = m.get("wiki_items") or []
    return sorted(i.get("title") or "" for i in items if i.get("title")), m


def fuzzy_match(a_titles, b_titles, thr=0.5):
    pairs = []
    used_b = set()
    for a in a_titles:
        best = None
        best_s = 0
        for i, b in enumerate(b_titles):
            if i in used_b:
                continue
            s = SequenceMatcher(None, a, b).ratio()
            if a in b or b in a:
                s = max(s, 0.75)
            if s > best_s:
                best_s = s
                best = (i, b)
        if best and best_s >= thr:
            used_b.add(best[0])
            pairs.append((a, best[1], best_s))
    return pairs


def cite_by_title(content):
    m = {}
    for p in Path(content).rglob("*.md"):
        t = p.read_text(encoding="utf-8", errors="ignore")
        hm = re.search(r"^#\s+(.+)$", t, re.M)
        title = hm.group(1).strip() if hm else p.stem
        m[title] = set(re.findall(r"file://([^)\s#]+)", t))
    return m


def heads(content, needle):
    for p in Path(content).rglob("*.md"):
        if needle in p.name or needle in str(p):
            t = p.read_text(encoding="utf-8", errors="ignore")
            return str(p.relative_to(content)).replace("\\", "/"), re.findall(
                r"^#{1,3}\s+.+", t, re.M
            )[:25]
    return None, []


def extend_stats(m):
    items = m.get("wiki_items") or []
    nonempty = 0
    keys = collections.Counter()
    dep = 0
    tr = collections.Counter()
    st = collections.Counter()
    for it in items:
        st[it.get("progress_status") or "?"] += 1
        ext = it.get("extend")
        ej = None
        if isinstance(ext, str) and ext.strip() and ext.strip() != "{}":
            try:
                ej = json.loads(ext)
            except Exception:
                keys["<bad>"] += 1
        elif isinstance(ext, dict) and ext:
            ej = ext
        if ej:
            nonempty += 1
            for k in ej:
                keys[k] += 1
            if ej.get("dependent_files"):
                dep += 1
            if ej.get("track"):
                tr[ej["track"]] += 1
    return nonempty, dict(keys), dep, dict(tr), dict(st)


def kstat(p):
    p = Path(p)
    if not p.exists():
        return "missing"
    files = [f for f in p.rglob("*") if f.is_file()]
    sample = [str(x.relative_to(p)).replace("\\", "/") for x in files[:6]]
    return f"files={len(files)} sample={sample}"


def main():
    qs = page_stats(ROOT / ".qoder/repowiki/zh/content")
    ws = page_stats(ROOT / ".wikify/content")
    qt, qm = titles_from_meta(ROOT / ".qoder/repowiki/zh/meta/repowiki-metadata.json")
    wt, wm = titles_from_meta(ROOT / ".wikify/meta/wiki-metadata.json")

    wiki = load(ROOT / ".wikify/meta/wiki.json")
    pages = wiki.get("pages") or []
    tracks = collections.Counter((p.get("track") or "") for p in pages)
    dep_pages = [p for p in pages if p.get("dependent_files")]
    dep_n = len(dep_pages)
    role = collections.Counter()
    for p in dep_pages:
        for f in p.get("dependent_files") or []:
            fl = f.lower()
            if fl.endswith("pom.xml"):
                role["pom"] += 1
            elif "controller" in fl:
                role["controller"] += 1
            elif "service" in fl:
                role["service"] += 1
            elif "entity" in fl or "model" in fl:
                role["entity"] += 1
            elif fl.endswith(".java"):
                role["java_other"] += 1
            elif fl.endswith((".js", ".css")):
                role["frontend"] += 1
            elif fl.endswith((".xml", ".properties", ".yml", ".yaml")):
                role["config"] += 1
            else:
                role["other"] += 1

    q_sec = collections.Counter()
    for p in Path(ROOT / ".qoder/repowiki/zh/content").rglob("*.md"):
        q_sec[p.relative_to(ROOT / ".qoder/repowiki/zh/content").parts[0]] += 1
    w_sec = collections.Counter()
    for p in pages:
        w_sec[p.get("section") or "(none)"] += 1

    pairs = fuzzy_match(qt, wt, 0.5)
    pairs_hi = [p for p in pairs if p[2] >= 0.65]
    pairs_mid = [p for p in pairs if 0.5 <= p[2] < 0.65]

    qc = cite_by_title(ROOT / ".qoder/repowiki/zh/content")
    wc = cite_by_title(ROOT / ".wikify/content")
    jacc = []
    for a, b, s in pairs_hi:
        ca, cb = qc.get(a, set()), wc.get(b, set())
        if not ca and not cb:
            continue
        j = len(ca & cb) / max(1, len(ca | cb))
        jacc.append((a, b, s, j, len(ca), len(cb), len(ca & cb)))
    jacc.sort(key=lambda x: -x[3])

    qp, qh = heads(ROOT / ".qoder/repowiki/zh/content", "内部审计")
    wp, wh = heads(ROOT / ".wikify/content", "内部审计")

    q_ne, q_ek, q_dep, q_tr, q_st = extend_stats(qm)
    w_ne, w_ek, w_dep, w_tr, w_st = extend_stats(wm)

    browse = 0
    if (ROOT / ".wikify/meta/browse-index.json").exists():
        browse = len(load(ROOT / ".wikify/meta/browse-index.json").get("pages") or [])

    both_tops = sorted(set(qs["tops"]) & set(ws["tops"]))
    only_q_tops = sorted(set(qs["tops"]) - set(ws["tops"]))
    only_w_tops = sorted(set(ws["tops"]) - set(qs["tops"]))

    lines = []
    A = lines.append
    A("# ADS com.sunyard.common：Qoder vs wikify 同仓对比")
    A("")
    A("路径: `F:/ALDrive/9月资源/ADS/com.sunyard.common`")
    A("")
    A(f"- Qoder: `.qoder/repowiki/`（md={qs['md']}，约 2026-06-29）")
    A(f"- wikify: `.wikify/`（md={ws['md']}，2026-07-23 全量 generate）")
    A("")
    A("## 1. 规模与格式信号")
    A("")
    A("| 指标 | Qoder | wikify |")
    A("|--|--:|--:|")
    A(f"| content md | {qs['md']} | {ws['md']} |")
    A(f"| wiki_items | {len(qt)} | {len(wt)} |")
    A(
        f"| knowledge_relations | {len(qm.get('knowledge_relations') or [])} | "
        f"{len(wm.get('knowledge_relations') or [])} |"
    )
    A(
        f"| 页长 avg/median/min/max | "
        f"{qs['avg_len']}/{qs['median_len']}/{qs['min_len']}/{qs['max_len']} | "
        f"{ws['avg_len']}/{ws['median_len']}/{ws['min_len']}/{ws['max_len']} |"
    )
    A(f"| `<cite>` | {qs['cites']}/{qs['md']} | {ws['cites']}/{ws['md']} |")
    A(f"| `## 目录` | {qs['tocs']}/{qs['md']} | {ws['tocs']}/{ws['md']} |")
    A(f"| mermaid | {qs['mermaids']}/{qs['md']} | {ws['mermaids']}/{ws['md']} |")
    A(f"| file:// | {qs['fileuri']}/{qs['md']} | {ws['fileuri']}/{ws['md']} |")
    A(f"| file://#L | {qs['line_ranges']}/{qs['md']} | {ws['line_ranges']}/{ws['md']} |")
    A(f"| 相关页面 | {qs['related']}/{qs['md']} | {ws['related']}/{ws['md']} |")
    A(f"| stub | {qs['stubs']} | {ws['stubs']} |")
    A(f"| avg H2 | {qs['avg_h2']:.1f} | {ws['avg_h2']:.1f} |")
    A(f"| avg cite 文件数 | {qs['avg_cite']:.1f} | {ws['avg_cite']:.1f} |")
    A(f"| 深度分布 | `{qs['depths']}` | `{ws['depths']}` |")
    A("")
    A("## 2. 顶层章节（content 第一层）")
    A("")
    A(f"- **共有** ({len(both_tops)}): {', '.join(both_tops)}")
    A(f"- **仅 Qoder** ({len(only_q_tops)}): {', '.join(only_q_tops)}")
    A(f"- **仅 wikify** ({len(only_w_tops)}): {', '.join(only_w_tops)}")
    A("")
    A("### 各章页数")
    A("")
    A("| section | Qoder | wikify(section字段) |")
    A("|--|--:|--:|")
    for s in sorted(set(q_sec) | set(w_sec)):
        A(f"| {s} | {q_sec.get(s, 0)} | {w_sec.get(s, 0)} |")
    A("")
    A("## 3. 标题对照（精确 vs 模糊）")
    A("")
    A(f"- 精确标题交集: **{len(set(qt) & set(wt))}** / Q={len(qt)} W={len(wt)}")
    A(f"- 模糊匹配 ≥0.65: **{len(pairs_hi)}** 对")
    A(f"- 模糊匹配 0.50–0.65: **{len(pairs_mid)}** 对")
    A("")
    A("### 高相似度对（前 25）")
    A("")
    A("| Qoder | wikify | score |")
    A("|--|--|--:|")
    for a, b, s in sorted(pairs_hi, key=lambda x: -x[2])[:25]:
        A(f"| {a} | {b} | {s:.2f} |")
    A("")
    A("### 说明")
    A("")
    A("两侧 catalog **不是同一套标题命名**：Qoder 偏「模块总览 + 工程专题」")
    A("（API/安全/部署/FAQ）；wikify 偏「业务能力叙述 + 工程分层」")
    A("（后端应用架构、技术栈与工程基础、规则与制度…）。")
    A("因此不能按「同名页 diff」硬比，应看章节覆盖 + 模糊主题对齐 + 格式完备性。")
    A("")
    A("### 未模糊匹配的 Qoder 标题样例（20）")
    A("")
    matched_q = {a for a, _, _ in pairs}
    for t in [x for x in qt if x not in matched_q][:20]:
        A(f"- {t}")
    A("")
    A("### 未模糊匹配的 wikify 标题样例（20）")
    A("")
    matched_w = {b for _, b, _ in pairs}
    for t in [x for x in wt if x not in matched_w][:20]:
        A(f"- {t}")
    A("")
    A("## 4. 证据绑定与元数据")
    A("")
    A("| 项 | Qoder | wikify |")
    A("|--|--|--|")
    A(f"| metadata extend 非空 | {q_ne}/{len(qt)} | {w_ne}/{len(wt)} |")
    A(f"| extend 字段 | `{q_ek}` | `{w_ek}` |")
    A(f"| metadata dependent_files | {q_dep} | {w_dep} |")
    A(f"| **wiki.json dependent_files** | n/a | **{dep_n}/{len(pages)}** |")
    A(f"| wiki.json track | n/a | `{dict(tracks)}`（**全空**） |")
    A(f"| progress_status | `{q_st}` | `{w_st}` |")
    A(f"| browse-index | n/a | {browse} |")
    A(f"| dep 路径角色分布 | n/a | `{dict(role)}` |")
    A("")
    A("> **重要发现**：wikify 的 `wiki.json` 里 **120/120 页都有 dependent_files**，但")
    A("> 1) `track` 字段全部缺失；2) `wiki-metadata.json` 的 `extend` 全是 `{{}}`，")
    A("> `reference_count=0`。")
    A("> 说明本次 generate 写入 metadata 时 **未把 track/dep 回填进 extend**")
    A("> （与离线 e2e 小样行为不一致）。")
    A("> 建议对该仓跑一次 `wikify polish` 验证是否可修复 track + metadata extend。")
    A("")
    A("## 5. 模糊对齐页的 cite 路径重叠")
    A("")
    if jacc:
        avg = sum(x[3] for x in jacc) / len(jacc)
        A(f"在 {len(jacc)} 对高相似标题上，cite 路径平均 Jaccard=**{avg:.2f}**")
        A("")
        A("| Qoder | wikify | 标题分 | Jaccard | Q# | W# | ∩ |")
        A("|--|--|--:|--:|--:|--:|--:|")
        for row in jacc[:12]:
            A(
                f"| {row[0]} | {row[1]} | {row[2]:.2f} | {row[3]:.2f} | "
                f"{row[4]} | {row[5]} | {row[6]} |"
            )
        A("")
        A("较低:")
        A("")
        A("| Qoder | wikify | Jaccard |")
        A("|--|--|--:|")
        for row in jacc[-8:]:
            A(f"| {row[0]} | {row[1]} | {row[3]:.2f} |")
    else:
        A("无足够对齐对可计算。")
    A("")
    A("## 6. knowledge")
    A("")
    A(f"- Qoder: {kstat(ROOT / '.qoder/repowiki/knowledge')}")
    A(f"- wikify: {kstat(ROOT / '.wikify/knowledge')}")
    A("")
    A("## 7. 页面结构样例")
    A("")
    A(f"### Qoder `{qp}`")
    A("```")
    A("\n".join(qh))
    A("```")
    A("")
    A(f"### wikify `{wp}`")
    A("```")
    A("\n".join(wh))
    A("```")
    A("")
    A("## 8. 结论")
    A("")
    A("### 已对齐 / 不弱于 Qoder")
    A("1. **页数量级相当**（115 vs 120），远超骨架级")
    A("2. **页面格式信号全面达标**：cite / TOC / mermaid / file:// / #L ≈ 100%")
    A("3. **页均长度与 H2 数** 与 Qoder 同级甚至略长（avg ~16k vs ~14k，H2≈11）")
    A("4. metadata **六大顶层键**、PARENT_CHILD、progress completed")
    A(
        "5. 业务大模块顶层有交集：内部审计/客户/工作流/指标/知识/运营/风险/架构/"
        "项目概述/前端"
    )
    A("6. wiki.json 内 **dependent_files 全覆盖**（证据绑定在规划层已生效）")
    A("7. 少量「相关页面」导航（Qoder 正文无）")
    A("")
    A("### 主要差距 / 风险")
    A(
        f"1. **目录树命名体系不同**：精确标题交集为 0；模糊对齐约 {len(pairs_hi)} 对。"
        "不是「缺页」而是「切题角度不同」。"
    )
    A(
        "2. **Qoder 独有工程专题更全**：API 接口设计、安全架构、部署运维、"
        "性能优化、测试策略、FAQ、附录。"
    )
    A(
        "3. **wikify 独有**：后端应用架构、技术栈与工程基础、协作与支撑、"
        "规则与制度文件管理等（更偏分层/横切）。"
    )
    A("4. **track 未落盘**：wiki.json 与 metadata 均无有效 track（双轨导航会退化）。")
    A(
        "5. **metadata.extend 空**：dependent_files 只在 wiki.json，"
        "不在 repowiki 风格 metadata。"
    )
    A("6. **wikify knowledge 缺失**（本仓）；Qoder 有主题知识卡。")
    A("7. cite 路径与 Qoder 重叠偏低属预期（选源算法不同），需抽检业务相关性。")
    A("")
    A("### 建议动作（按优先级）")
    A("1. 在本仓执行 `wikify polish`，确认 track + metadata extend + knowledge 是否回填")
    A(
        "2. 若 polish 后仍空：修 Export 边界（EnsureTracks + 从 wiki.json "
        "dependent_files 写 extend）"
    )
    A(
        "3. Catalog 种子补齐通用工程专题（API/安全/部署/运维/测试/FAQ）—"
        "不写死 ADS 业务名"
    )
    A("4. 抽 5 个业务页人工看 dependent_files 是否合理（避免 pom 过载）")
    A("")

    OUT.write_text("\n".join(lines), encoding="utf-8")
    print("wrote", OUT)
    print("exact both", len(set(qt) & set(wt)), "fuzzy>=0.65", len(pairs_hi))
    print("dep wiki.json", dep_n, "role", dict(role))
    print("tracks", dict(tracks))
    print("meta extend nonempty Q/W", q_ne, w_ne)
    if jacc:
        print("jacc n", len(jacc), "avg", sum(x[3] for x in jacc) / len(jacc))
    print("related", qs["related"], ws["related"], "len", qs["avg_len"], ws["avg_len"])
    print("tops both", both_tops)
    print("onlyQ tops", only_q_tops)
    print("onlyW tops", only_w_tops)


if __name__ == "__main__":
    main()
