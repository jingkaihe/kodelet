local M = {}
local writer = require('kodelet.writer')

-- Track buffer changes and update context file
function M.setup_autocmds()
    local group = vim.api.nvim_create_augroup("KodeletContext", { clear = true })
    
    -- Update context when buffers change
    vim.api.nvim_create_autocmd({"BufEnter", "BufDelete", "BufWipeout"}, {
        group = group,
        callback = function()
            -- Debounce: only write after a short delay
            vim.defer_fn(function()
                if writer.conversation_id then
                    writer.write_context()
                end
            end, 200)
        end
    })
end

-- Send current visual selection to Kodelet
function M.send_selection()
    local start_pos = vim.fn.getpos("'<")
    local end_pos = vim.fn.getpos("'>")
    local filepath = vim.fn.expand("%:p")
    
    -- Get selected lines
    local lines = vim.api.nvim_buf_get_lines(
        0,
        start_pos[2] - 1,
        end_pos[2],
        false
    )
    
    local content = table.concat(lines, "\n")
    
    local selection_info = {
        file_path = filepath,
        start_line = start_pos[2],
        end_line = end_pos[2],
        content = content
    }
    
    -- Write context with selection
    if writer.write_context_with_selection(selection_info) then
        vim.notify("Selection added to Kodelet context", vim.log.levels.INFO)
    else
        vim.notify("Failed to add selection to context", vim.log.levels.ERROR)
    end
end

return M
