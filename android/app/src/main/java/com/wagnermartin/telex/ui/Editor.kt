package com.wagnermartin.telex.ui

import com.wagnermartin.telex.data.Node

private val EDITABLE = Regex(
    """.*\.(txt|text|md|markdown|csv|tsv|log|json|ya?ml|toml|ini|cfg|conf|env|properties|xml|html?|css|scss|js|mjs|cjs|ts|go|py|sh|bash|ps1|bat|cmd|sql|c|cpp|h|hpp|cs|java|kt|rs|php|rb|lua|srt|vtt|gitignore|dockerignore)""",
    RegexOption.IGNORE_CASE,
)
private val EDITABLE_NAME = Regex("""\.env(\..*)?|dockerfile|makefile|readme""", RegexOption.IGNORE_CASE)

/** The largest file the live editor opens, as in the web interface. */
const val EDIT_LIMIT = 5L shl 20

/** Notes, data and code files that open in the live editor (EditorActivity). */
fun editable(n: Node) = !n.isDir && (EDITABLE.matches(n.name) || EDITABLE_NAME.matches(n.name))
