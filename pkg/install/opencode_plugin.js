// enola for opencode. Written by `enola install --hooks`; removed by `enola uninstall`.
//
// opencode has no hook configuration in the shape enola writes for Claude Code and
// Codex, so the same job is done from a plugin. What it does is also narrower than
// those hooks, and answers a different question: not "what did this session change",
// but "did this session start by reading the map, or by grepping for it".
//
// That distinction is worth a plugin because it is where the time goes. An agent
// opening an unfamiliar repository reaches for grep and glob first, and each of those
// is a round trip plus the files it decides to read afterwards. On a local model that
// is the difference between one answer and forty minutes of file reading. enola
// answers the same structural question from an index, once.
//
// Three mechanisms, weakest first, because the weak ones cost nothing and only the
// strong one can be wrong:
//
//   1. tool.definition - grep, glob and list carry one extra line naming the enola
//      tool that answers the structural version of their question. Models choose
//      tools by reading descriptions, so this is the cheapest intervention there is.
//   2. experimental.chat.system.transform - the same instruction in the system
//      prompt, where it also reaches subagents. A subagent never reads the project
//      instruction file, and a subagent is exactly where the blind searching happens.
//   3. tool.execute.before - the gate. The first searches of a session fail with a
//      message naming the enola tool to call instead. This one genuinely blocks, so
//      it is bounded twice over: it gives up after GATE_BUDGET refusals, and it gives
//      up the moment any enola tool is called, including one that failed. An agent
//      that cannot get an answer out of enola must always be able to fall back.

// Searching tools. `read` is deliberately absent: reading a file you have already
// located is not the thing enola replaces, and blocking it breaks every session.
const SEARCH_TOOLS = new Set(["grep", "glob", "list"])

// How many searches one session may have refused before the gate gives up. Two is
// enough to change the opening move, and small enough that a session where enola
// turns out to be unhelpful loses seconds rather than minutes.
const GATE_BUDGET = 2

// What to reach for instead, per tool. The name is kept separate from the sentence so
// it can be rendered with delimiters around it: a weak model reading `enola_explore,
// which lists...` has been observed calling a tool named `enola_explore?`, having taken
// the punctuation after the name as part of it.
const ALTERNATIVE = {
  grep: { tool: "explore", why: "for structure, or query_facts for a named symbol, route or file" },
  glob: { tool: "explore", why: "which lists what is actually there without guessing at filenames" },
  list: { tool: "explore", why: "which reports modules and how they relate rather than a directory" },
}

export default async () => {
  // MCP server names serving enola. Empty means enola is not configured in this
  // opencode at all, and every mechanism below turns itself off: an agent redirected
  // to a tool that does not exist is strictly worse off than one left alone.
  let servers = []
  const sessions = new Map()

  const enabled = () => servers.length > 0
  const gateOn = () => enabled() && process.env.ENOLA_OPENCODE_GATE !== "off"

  // opencode names an MCP tool `<server>_<tool>`, and the server name is the user's
  // to choose, so the prefix is discovered rather than assumed.
  const prefix = () => servers[0] + "_"
  const isEnolaTool = (id) => servers.some((s) => id.startsWith(s + "_"))

  const stateOf = (id) => {
    let s = sessions.get(id)
    if (!s) {
      s = { refusals: 0, satisfied: false }
      sessions.set(id, s)
    }
    return s
  }

  return {
    // Once, with the merged config: the only place the set of MCP servers is known
    // without an HTTP round trip that could race their startup.
    config: async (cfg) => {
      const found = []
      for (const [name, server] of Object.entries(cfg?.mcp ?? {})) {
        if (!server || server.enabled === false) continue
        const argv = Array.isArray(server.command) ? server.command : []
        const binary = String(argv[0] ?? "").split(/[\\/]/).pop().replace(/\.exe$/i, "")
        // Either half is enough: the user may rename the server, or point a server
        // named something else at the enola binary.
        if (binary === "enola" || binary.startsWith("enola-") || name === "enola" || name.startsWith("enola-")) {
          found.push(name)
        }
      }
      // Shortest first, so the message names `enola_explore` rather than a wrapper's
      // longer prefix when both are configured.
      servers = found.sort((a, b) => a.length - b.length || a.localeCompare(b))
    },

    "tool.definition": async (input, output) => {
      if (!enabled() || !SEARCH_TOOLS.has(input.toolID)) return
      const alt = ALTERNATIVE[input.toolID]
      output.description =
        `${output.description}\n\nThis repository is indexed by enola. For where something lives, ` +
        `what depends on it, or how it is wired, call \`${prefix()}${alt.tool}\` first (${alt.why}) - ` +
        `it is exact, and it costs a fraction of the file reading it replaces. Use this tool for ` +
        `what enola does not carry: text inside a file, or a file you already know the path of.`
    },

    "experimental.chat.system.transform": async (_input, output) => {
      if (!enabled()) return
      const p = prefix()
      output.system.push(
        `enola serves this codebase's structure as a queryable index: modules, symbols, routes, ` +
          `storage, and how they depend on each other. Before grep, glob or reading files to work ` +
          `out how something is wired, call ${p}explore, ${p}query_facts, ${p}traverse or ` +
          `${p}impact_analysis. If enola reports no facts for this repository, call ` +
          `${p}generate_snapshot once and continue. Read files for what enola does not carry: the ` +
          `contents of code you have already located.`,
      )
    },

    "tool.execute.before": async (input) => {
      if (!gateOn()) return
      const s = stateOf(input.sessionID)

      // Any enola call satisfies the session, including one that failed. The gate
      // exists to change the first move, not to keep score, and a session that cannot
      // get an answer out of enola must not be held there.
      if (isEnolaTool(input.tool)) {
        s.satisfied = true
        return
      }
      if (s.satisfied || !SEARCH_TOOLS.has(input.tool) || s.refusals >= GATE_BUDGET) return

      s.refusals += 1
      const p = prefix()
      const alt = ALTERNATIVE[input.tool]
      throw new Error(
        `enola has not been consulted in this session, so ${input.tool} was not run.\n\n` +
          `Call the tool named \`${p}${alt.tool}\` (${alt.why}). If it reports no facts for this ` +
          `repository, call \`${p}generate_snapshot\` once, then continue.\n\n` +
          `This redirect is bounded: at most ${GATE_BUDGET} per session, and it stops entirely as ` +
          `soon as any enola tool is called, so ${input.tool} is available again immediately after.`,
      )
    },
  }
}
