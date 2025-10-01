local M = {}
local writer = require('kodelet.writer')

-- Timer for debouncing
local update_timer = nil

-- Track buffer changes and update context file
function M.setup_autocmds()
    local group = vim.api.nvim_create_augroup("KodeletContext", { clear = true })
    
    -- Update context when buffers change (but not immediately after selection)
    vim.api.nvim_create_autocmd({"BufEnter", "BufDelete", "BufWipeout"}, {
        group = group,
        callback = function()
            -- Cancel any pending timer
            if update_timer then
                vim.fn.timer_stop(update_timer)
            end
            
            -- Debounce: only write after a delay
            update_timer = vim.fn.timer_start(500, function()
                if writer.conversation_id then
                    writer.write_context()
                end
            end)
        end
    })
end

-- Send current visual selection to Kodelet
function M.send_selection()
    -- Get visual selection marks
    local start_pos = vim.fn.getpos("'<")
    local end_pos = vim.fn.getpos("'>")
    local filepath = vim.fn.expand("%:p")
    
    -- Extract line and column numbers (1-indexed)
    local start_line = start_pos[2]
    local start_col = start_pos[3]
    local end_line = end_pos[2]
    local end_col = end_pos[3]
    
    -- Get the selected lines (0-indexed API)
    local lines = vim.api.nvim_buf_get_lines(
        0,
        start_line - 1,
        end_line,
        false
    )
    
    if #lines == 0 then
        vim.notify("No selection found", vim.log.levels.WARN)
        return
    end
    
    -- Handle single line selection
    if #lines == 1 then
        -- Extract substring from start_col to end_col
        lines[1] = string.sub(lines[1], start_col, end_col)
    else
        -- Multi-line selection: trim first and last lines
        lines[1] = string.sub(lines[1], start_col)
        lines[#lines] = string.sub(lines[#lines], 1, end_col)
    end
    
    local content = table.concat(lines, "\n")
    
    -- Validate we have content
    if content == "" or content == nil then
        vim.notify("Selection is empty", vim.log.levels.WARN)
        return
    end
    
    local selection_info = {
        file_path = filepath,
        start_line = start_line,
        end_line = end_line,
        content = content
    }
    
    -- Write context with selection
    if writer.write_context_with_selection(selection_info) then
        vim.notify("Selection added to Kodelet context (" .. #lines .. " lines)", vim.log.levels.INFO)
    else
        vim.notify("Failed to add selection to context", vim.log.levels.ERROR)
    end
end

return M
