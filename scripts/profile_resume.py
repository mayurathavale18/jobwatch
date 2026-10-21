"""Render profile-owned content through a candidate-neutral LaTeX template."""
from collections import defaultdict
import re

import profile_runtime as pr


def escape(value):
    replacements = {"\\": r"\textbackslash{}", "&": r"\&", "%": r"\%", "$": r"\$", "#": r"\#",
                    "_": r"\_", "{": r"\{", "}": r"\}", "~": r"\textasciitilde{}", "^": r"\textasciicircum{}"}
    return "".join(replacements.get(char, char) for char in str(value))


def header():
    identity = pr.require_identity(resume=True)
    contacts = [identity.get(key, "") for key in ("location", "phone", "email")]
    links = [r"\href{" + escape(link["url"]) + "}{" + escape(link["label"]) + "}"
             for link in identity.get("links", [])]
    return (r"\begin{center}{\Huge \scshape " + escape(identity["name"]) + r"} \\" + "\n" +
            r" \textbullet\ ".join(escape(item) for item in contacts if item) + r" \\" + "\n" +
            r" \textbullet\ ".join(links) + "\n" + r"\end{center}")


def categories(jd_keywords):
    result = defaultdict(list)
    for skill in pr.skills():
        if skill["level"] == "used":
            result[skill.get("category") or "Skills"].append(skill["name"])
    return {category: ", ".join(names) for category, names in result.items()}


def experience(focus, jd_keywords, tight=False):
    ranked = []
    for role in pr.data().get("background", {}).get("experience", []):
        for fact in pr.statements(role):
            score = sum(3 for tag in fact.get("tags", []) if tag in focus)
            score += sum(1 for keyword in jd_keywords if re.search(r"\b" + re.escape(keyword) + r"\b", fact["text"], re.I))
            ranked.append((score, role["id"], fact["text"]))
    ranked.sort(key=lambda item: item[0], reverse=True)
    limit = 5 if tight else (6 if "observability" in focus or len(focus) > 2 else 7)
    return [{"id": role["id"], "bullets": [text for _, key, text in ranked[:limit] if key == role["id"]]}
            for role in pr.data().get("background", {}).get("experience", [])
            if any(key == role["id"] for _, key, _ in ranked[:limit])]


def projects():
    result = []
    for project in pr.data().get("background", {}).get("projects", []):
        if project["status"] != "confirmed":
            continue
        name = escape(project["name"])
        if project.get("url"):
            name = r"\href{" + escape(project["url"]) + "}{" + name + "}"
        description = " ".join(escape(item["text"]) for item in pr.statements(project))
        result.append(r"\resumeItem{\textbf{" + name + "}" + (" --- " + description if description else "") + "}")
    return "\n".join(result)


def dates(item):
    return escape(item["startDate"]) + " -- " + escape(item.get("endDate") or "Present")


def render(template, skills, roles, projects_text):
    known = {role["id"]: role for role in pr.data().get("background", {}).get("experience", [])}
    experience_tex = []
    for block in roles:
        role = known[block["id"]]
        experience_tex.append(r"\resumeSubheading{" + escape(role["title"]) + "}{" + dates(role) + "}{" +
                              escape(role["company"]) + "}{" + escape(role.get("location", "")) + "}\n" +
                              r"\resumeItemListStart" + "\n" +
                              "\n".join(r"\resumeItem{" + escape(text) + "}" for text in block["bullets"]) +
                              "\n" + r"\resumeItemListEnd")
    education = [r"\resumeSubheading{" + escape(item["institution"]) + "}{" + dates(item) + "}{" +
                 escape(item["qualification"]) + "}{}"
                 for item in pr.data().get("background", {}).get("education", [])]
    sections = {"HEADER": header(), "SKILLS": skills,
                "EXPERIENCE": (r"\section{Experience}\begin{itemize}[leftmargin=0pt,label={}]" + "\n" +
                               "\n".join(experience_tex) + "\n" + r"\end{itemize}") if experience_tex else "",
                "PROJECTS": (r"\section{Projects \& Writing}\resumeItemListStart" + "\n" + projects_text +
                             "\n" + r"\resumeItemListEnd") if projects_text else "",
                "EDUCATION": (r"\section{Education}\begin{itemize}[leftmargin=0pt,label={}]" + "\n" +
                              "\n".join(education) + "\n" + r"\end{itemize}") if education else ""}
    for key, value in sections.items():
        marker = "%%JOBWATCH_" + key + "%%"
        if template.count(marker) != 1:
            raise pr.ProfileError("Resume template must contain each Jobwatch marker exactly once.")
        template = template.replace(marker, value)
    return template
