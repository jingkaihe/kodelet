local M = {}

M.conversation_id = nil
M.ide_dir = vim.fn.expand("~/.kodelet/ide")

function M.set_conversation_id(conv_id)
    M.conversation_id = conv_id
    -- Ensure IDE directory exists
    vim.fn.mkdir(M.ide_dir, "p")
end

function M.get_context_path()
    if not M.conversation_id then
        return nil
    end
    return M.ide_dir .. "/context-" .. M.conversation_id .. ".json"
end

-- Gather current IDE context
function M.gather_context(include_diagnostics)
    local open_files = {}
    local buffers = vim.api.nvim_list_bufs()

    for _, buf in ipairs(buffers) do
        if vim.api.nvim_buf_is_loaded(buf) and vim.bo[buf].buflisted then
            local filepath = vim.api.nvim_buf_get_name(buf)
            if filepath ~= "" and vim.fn.filereadable(filepath) == 1 then
                table.insert(open_files, {
                    path = filepath,
                    language = vim.bo[buf].filetype
                })
            end
        end
    end

    local context = {
        open_files = open_files,
        updated_at = os.date("!%Y-%m-%dT%H:%M:%SZ")
    }

    -- Optionally include diagnostics
    if include_diagnostics then
        context.diagnostics = M.gather_diagnostics()
    end

    return context
end

-- Gather diagnostics from all open buffers
function M.gather_diagnostics()
    local diagnostics = {}
    local buffers = vim.api.nvim_list_bufs()

    for _, buf in ipairs(buffers) do
        if vim.api.nvim_buf_is_loaded(buf) then
            local filepath = vim.api.nvim_buf_get_name(buf)
            if filepath ~= "" then
                local buf_diagnostics = vim.diagnostic.get(buf)

                for _, diag in ipairs(buf_diagnostics) do
                    local severity_map = {
                        [vim.diagnostic.severity.ERROR] = "error",
                        [vim.diagnostic.severity.WARN] = "warning",
                        [vim.diagnostic.severity.INFO] = "info",
                        [vim.diagnostic.severity.HINT] = "hint"
                    }

                    table.insert(diagnostics, {
                        file_path = filepath,
                        line = diag.lnum + 1,  -- Convert 0-indexed to 1-indexed
                        column = diag.col + 1,
                        severity = severity_map[diag.severity] or "info",
                        message = diag.message,
                        source = diag.source or "",
                        code = diag.code or ""
                    })
                end
            end
        end
    end

    return diagnostics
end

-- Write IDE context to file (always includes diagnostics)
-- This function preserves any existing selection in the file
function M.write_context()
    if not M.conversation_id then
        vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        return false
    end

    local context = M.gather_context(true)  -- Always include diagnostics
    local context_path = M.get_context_path()
    if not context_path then
        vim.notify("Failed to get context path", vim.log.levels.ERROR)
        return false
    end

    -- Read existing context to preserve selection field
    if vim.fn.filereadable(context_path) == 1 then
        local existing_content = vim.fn.readfile(context_path)
        if existing_content and #existing_content > 0 and existing_content[1] then
            local ok, existing_context = pcall(vim.fn.json_decode, existing_content[1])
            if ok and existing_context and existing_context.selection then
                -- Preserve the existing selection
                context.selection = existing_context.selection
            end
        end
    end

    local json_str = vim.fn.json_encode(context)
    local success = vim.fn.writefile({json_str}, context_path)

    if success == 0 then
        return true
    else
        vim.notify("Failed to write IDE context", vim.log.levels.ERROR)
        return false
    end
end

-- Update context with selection (always includes diagnostics)
function M.write_context_with_selection(selection_info)
    if not M.conversation_id then
        vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        return false
    end

    local context = M.gather_context(true)  -- Include diagnostics
    context.selection = selection_info

    local context_path = M.get_context_path()
    if not context_path then
        vim.notify("Failed to get context path", vim.log.levels.ERROR)
        return false
    end

    local json_str = vim.fn.json_encode(context)

    -- Write to file with proper line splitting for readability
    local success = vim.fn.writefile({json_str}, context_path)

    if success == 0 then
        vim.notify("Selection written to: " .. context_path, vim.log.levels.INFO)
        return true
    else
        vim.notify("Failed to write selection (code: " .. tostring(success) .. ")", vim.log.levels.ERROR)
        return false
    end
end

-- Clear IDE context file
function M.clear_context()
    if not M.conversation_id then
        return false
    end

    local context_path = M.get_context_path()
    if not context_path then
        return false
    end

    if vim.fn.filereadable(context_path) == 1 then
        vim.fn.delete(context_path)
        return true
    end
    return false
end

-- Clear only the selection from context (keep open files and diagnostics)
function M.clear_selection()
    if not M.conversation_id then
        vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        return false
    end

    local context_path = M.get_context_path()
    if not context_path then
        vim.notify("Failed to get context path", vim.log.levels.ERROR)
        return false
    end

    -- Read existing context
    if vim.fn.filereadable(context_path) == 1 then
        local existing_content = vim.fn.readfile(context_path)
        if existing_content and #existing_content > 0 and existing_content[1] then
            local ok, existing_context = pcall(vim.fn.json_decode, existing_content[1])
            if ok and existing_context then
                -- Remove selection field
                existing_context.selection = vim.NIL  -- Use vim.NIL to remove from JSON

                local json_str = vim.fn.json_encode(existing_context)
                local success = vim.fn.writefile({json_str}, context_path)

                if success == 0 then
                    vim.notify("Selection cleared from context", vim.log.levels.INFO)
                    return true
                end
            end
        end
    end

    vim.notify("No selection to clear", vim.log.levels.INFO)
    return false
end

return M
