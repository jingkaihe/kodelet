import type { ToolInventory } from '../components/chat/toolInventory';

const pluginRoot = '~/.kodelet/plugins';

export const sampleToolInventory: ToolInventory = {
  builtinTools: [
    {
      name: 'bash',
      description: 'Run a bash command and return its combined output and exit code.',
    },
    {
      name: 'browser',
      description: 'Open, navigate, evaluate, or screenshot the browser page shared with the user.',
    },
    {
      name: 'code_execute',
      description:
        'Run the body of an async JavaScript function in a sandboxed VM; top-level await and return work.',
    },
    {
      name: 'file_edit',
      description:
        'Replace exact text in a file; old_text must be unique unless replace_all is set.',
    },
    { name: 'file_write', description: 'Create or overwrite a file with the given text.' },
    {
      name: 'skill',
      description: 'Load a skill’s instructions and supporting files when the task calls for it.',
    },
    {
      name: 'view_image',
      description: 'View an image from a local path or a tool-returned artifact ID.',
    },
    {
      name: 'web_fetch',
      description: 'Fetch a public URL, optionally extracting information with a prompt.',
    },
    {
      name: 'apply_patch',
      description: 'Apply a structured patch that adds, updates, or deletes files.',
      enabled: false,
    },
    { name: 'file_read', description: 'Read a file with line numbers.', enabled: false },
    { name: 'glob_tool', description: 'Find files by glob pattern.', enabled: false },
    {
      name: 'grep_tool',
      description: 'Search file contents with a regular expression.',
      enabled: false,
    },
  ],
  plugins: [
    {
      name: 'jingkaihe@kodelet',
      scope: 'global',
      path: `${pluginRoot}/jingkaihe@kodelet`,
      extensions: [],
      skills: [
        {
          name: 'kodelet',
          description:
            'Use when working with Kodelet CLI usage, configuration, recipes, skills, plugins, extensions, or the TypeScript and Go SDKs.',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@kodelet/skills/kodelet`,
        },
      ],
    },
    {
      name: 'jingkaihe@skills',
      scope: 'global',
      path: `${pluginRoot}/jingkaihe@skills`,
      extensions: [
        {
          id: 'jingkaihe@skills/caffeinate',
          name: 'caffeinate',
          version: '0.1.0',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/caffeinate`,
          status: 'ready',
          tools: [],
          hooks: ['agent.start', 'turn.start', 'agent.finished', 'session.end'],
        },
        {
          id: 'jingkaihe@skills/code-search',
          name: 'code-search',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/code-search`,
          status: 'ready',
          tools: [
            {
              name: 'code_search',
              description:
                'Find code by behavior or concept and return relevant file and line references.',
            },
          ],
        },
        {
          id: 'jingkaihe@skills/conversation',
          name: 'conversation',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/conversation`,
          status: 'ready',
          tools: [
            {
              name: 'read_conversation',
              description: 'Read a saved conversation and extract evidence relevant to a goal.',
            },
            {
              name: 'search_conversation',
              description:
                'Find saved conversations by keyword, returning ranked IDs and excerpts.',
            },
          ],
        },
        {
          id: 'jingkaihe@skills/last-word',
          name: 'last-word',
          version: '0.1.0',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/last-word`,
          status: 'ready',
          tools: [],
          commands: ['/last-word'],
          shortcuts: ['ctrl+alt+w'],
          hooks: ['turn.end'],
        },
        {
          id: 'jingkaihe@skills/look-at',
          name: 'look-at',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/look-at`,
          status: 'ready',
          tools: [
            {
              name: 'look_at',
              description:
                'Extract targeted information from local documents, images, audio, or video.',
            },
          ],
        },
        {
          id: 'jingkaihe@skills/nano-banana',
          name: 'nano-banana',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/nano-banana`,
          status: 'ready',
          tools: [
            {
              name: 'nano_banana',
              description: 'Generate an image from a prompt and return it as an image attachment.',
            },
          ],
        },
        {
          id: 'jingkaihe@skills/web-search',
          name: 'web-search',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@skills/extensions/web-search`,
          status: 'ready',
          tools: [
            {
              name: 'web_search',
              description:
                'Research a question on the web and return an answer with source citations.',
            },
          ],
        },
      ],
      skills: [
        ['cloudflare-tunnel', 'Expose localhost or a private service through Cloudflare Tunnel.'],
        [
          'extension-config',
          'Configure jingkaihe/skills Kodelet extensions through extensions.config.',
        ],
        [
          'google-workspace',
          'Work with Gmail, Calendar, Contacts, or Drive through the MCP server.',
        ],
        ['icloud-cli', 'Manage iCloud calendars, events, and email using the icloud CLI.'],
        ['librarian', 'Maintain a local cache of remote Git repositories for code research.'],
        ['matchlock', 'Run AI agents and arbitrary code in ephemeral micro-VMs.'],
        ['screen-recording', 'Record terminal demos, browser screencasts, and TUI walkthroughs.'],
        [
          'strix-halo-llm',
          'Run, benchmark, and serve local GGUF / llama.cpp models on Strix Halo.',
        ],
        ['tmux', 'Run interactive CLIs and long-running tasks in isolated tmux sessions.'],
        ['uv', 'Use uv for Python packages, environments, and self-contained scripts.'],
        ['waitrose-cli', 'Manage Waitrose grocery shopping using the waitrose CLI.'],
      ].map(([name, description]) => ({
        name,
        description,
        scope: 'global' as const,
        path: `${pluginRoot}/jingkaihe@skills/skills/${name}`,
      })),
    },
    {
      name: 'jingkaihe@thirdspace',
      scope: 'global',
      path: `${pluginRoot}/jingkaihe@thirdspace`,
      extensions: [],
      skills: [
        {
          name: 'thirdspace',
          description: 'Manage Third Space gym classes with the thirdspace CLI.',
          scope: 'global',
          path: `${pluginRoot}/jingkaihe@thirdspace/skills/thirdspace`,
        },
      ],
    },
    {
      name: 'typesafe-ai@skills',
      scope: 'global',
      path: `${pluginRoot}/typesafe-ai@skills`,
      extensions: [],
      skills: [
        {
          name: 'typesafe-ai',
          description:
            'Build AI-powered software with TypeSafe: small units of AI intelligence you can use like programming primitives.',
          scope: 'global',
          path: `${pluginRoot}/typesafe-ai@skills/skills/typesafe-ai`,
        },
      ],
    },
  ],
  extensions: [
    {
      id: 'subagent',
      name: 'subagent',
      scope: 'path',
      path: '~/.local/bin/kodelet-extension-subagent',
      status: 'ready',
      tools: [
        ['spawn_agent', 'Start a named Kodelet agent in the background and return immediately.'],
        ['list_agents', 'List background agents with their current status and latest run.'],
        ['wait_agent', 'Wait for a background agent’s current run.'],
        ['followup_agent', 'Give an idle, failed, or canceled background agent a new task.'],
        ['steer_agent', 'Send guidance to a background agent that is currently running.'],
        ['cancel_agent', 'Cancel a background agent.'],
        ['list_agent_contacts', 'Discover agent contacts for inter-agent messaging.'],
        ['list_agent_messages', 'List agent inbox headers and team updates.'],
        ['read_agent_messages', 'Read selected agent inbox messages and mark them read.'],
        ['send_agent_message', 'Send an inter-agent inbox message to the recipient.'],
      ].map(([name, description]) => ({ name, description })),
    },
  ],
  skills: [
    {
      name: 'self-test',
      description:
        'Build and test Kodelet changes with an isolated server, CLI, and shared browser.',
      scope: 'repo',
      path: '.kodelet/skills/self-test',
    },
  ],
};

export const toolInventoryWithIssues: ToolInventory = {
  ...sampleToolInventory,
  plugins: sampleToolInventory.plugins.map((plugin) =>
    plugin.name === 'jingkaihe@skills'
      ? {
          ...plugin,
          skills: plugin.skills.map((skill) =>
            skill.name === 'strix-halo-llm' ? { ...skill, enabled: false } : skill
          ),
        }
      : plugin
  ),
  extensions: [
    ...sampleToolInventory.extensions,
    {
      id: 'drawing-board',
      name: 'drawing-board',
      scope: 'repo',
      path: '.kodelet/extensions/drawing-board',
      status: 'failed',
      error: 'Exited during initialize (status 1): Cannot find module ‘kodelet’.',
      tools: [],
    },
  ],
};

export const builtinOnlyToolInventory: ToolInventory = {
  builtinTools: sampleToolInventory.builtinTools,
  plugins: [],
  extensions: [],
  skills: [],
};
