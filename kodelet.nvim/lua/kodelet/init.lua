local M = {}

function M.setup(opts)
    opts = opts or {}

    -- Setup commands
    require('kodelet.commands').setup()

    -- Setup context tracking (only if attached to a session)
    require('kodelet.context').setup_autocmds()

    -- Auto-attach if KODELET_CONVERSATION_ID env var is set
    local env_conv_id = vim.env.KODELET_CONVERSATION_ID
    if env_conv_id and env_conv_id ~= "" then
        vim.defer_fn(function()
            local writer = require('kodelet.writer')
            writer.set_conversation_id(env_conv_id)
            writer.write_context()
            vim.notify("Auto-attached to Kodelet session: " .. env_conv_id, vim.log.levels.INFO)
        end, 100)
    end
end

return M
