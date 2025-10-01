local M = {}
local writer = require('kodelet.writer')

-- Fetch conversation list for completion
local function fetch_conversations()
    local handle = io.popen("kodelet conversation list --json 2>/dev/null")
    if not handle then
        return {}
    end
    
    local output = handle:read("*a")
    handle:close()
    
    if output == "" then
        return {}
    end
    
    local ok, data = pcall(vim.fn.json_decode, output)
    if not ok or type(data) ~= "table" then
        return {}
    end
    
    -- Extract conversations array from the response
    local conversations = data.conversations or {}
    
    return conversations
end

-- Completion function for KodeletAttach
local function complete_conversation_id(arg_lead, cmd_line, cursor_pos)
    local conversations = fetch_conversations()
    local completions = {}
    
    for _, conv in ipairs(conversations) do
        if conv.id and conv.preview then
            -- Format: "ID    summary"
            local completion = string.format("%s\t%s", conv.id, conv.preview)
            if vim.startswith(conv.id, arg_lead) then
                table.insert(completions, completion)
            end
        elseif conv.id then
            if vim.startswith(conv.id, arg_lead) then
                table.insert(completions, conv.id)
            end
        end
    end
    
    return completions
end

function M.setup()
    -- Attach to Kodelet session with tab completion
    vim.api.nvim_create_user_command("KodeletAttach", function(args)
        local conversation_id = args.args
        
        if conversation_id == "" then
            -- Try to find the most recent conversation
            local conversations = fetch_conversations()
            
            if #conversations == 0 then
                vim.notify("No conversations found", vim.log.levels.WARN)
                return
            end
            
            -- Use most recent
            if conversations[1] and conversations[1].id then
                conversation_id = conversations[1].id
            else
                vim.notify("No conversation ID found", vim.log.levels.ERROR)
                return
            end
        else
            -- Extract just the ID if user selected with summary (ID\tsummary)
            local id_part = vim.split(conversation_id, "\t")[1]
            conversation_id = id_part
        end
        
        if conversation_id ~= "" then
            writer.set_conversation_id(conversation_id)
            writer.write_context()
            vim.notify("Attached to Kodelet session: " .. conversation_id, vim.log.levels.INFO)
        else
            vim.notify("No conversation ID provided or found", vim.log.levels.ERROR)
        end
    end, { 
        nargs = "?",
        complete = complete_conversation_id,
        desc = "Attach to a Kodelet conversation (tab to see list)"
    })
    
    -- Alternative: Use vim.ui.select for interactive picker
    vim.api.nvim_create_user_command("KodeletAttachSelect", function()
        local conversations = fetch_conversations()
        
        if #conversations == 0 then
            vim.notify("No conversations found", vim.log.levels.WARN)
            return
        end
        
        -- Format for display
        local items = {}
        local id_map = {}
        for i, conv in ipairs(conversations) do
            local label = conv.id
            if conv.preview then
                label = string.format("%s - %s", conv.id, conv.preview)
            end
            items[i] = label
            id_map[i] = conv.id
        end
        
        vim.ui.select(items, {
            prompt = "Select Kodelet conversation:",
            format_item = function(item)
                return item
            end,
        }, function(choice, idx)
            if idx then
                local conversation_id = id_map[idx]
                writer.set_conversation_id(conversation_id)
                writer.write_context()
                vim.notify("Attached to Kodelet session: " .. conversation_id, vim.log.levels.INFO)
            end
        end)
    end, { desc = "Attach to Kodelet conversation using picker" })
    
    -- Send feedback message to Kodelet using the CLI
    vim.api.nvim_create_user_command("KodeletFeedback", function(args)
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session. Use :KodeletAttach first", vim.log.levels.WARN)
            return
        end
        
        local message = args.args
        if message == "" then
            message = vim.fn.input("Feedback message: ")
        end
        
        if message ~= "" then
            -- Use kodelet feedback CLI
            local escaped_msg = message:gsub("'", "'\\''")
            local cmd = string.format("kodelet feedback --conversation-id %s '%s' 2>&1", 
                writer.conversation_id, escaped_msg)
            
            local handle = io.popen(cmd)
            if handle then
                local output = handle:read("*a")
                local success = handle:close()
                
                if success then
                    vim.notify("Feedback sent to Kodelet", vim.log.levels.INFO)
                else
                    vim.notify("Failed to send feedback: " .. output, vim.log.levels.ERROR)
                end
            end
        end
    end, { nargs = "*", desc = "Send feedback to Kodelet" })
    
    -- Send visual selection
    vim.api.nvim_create_user_command("KodeletSendSelection", function()
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session. Use :KodeletAttach first", vim.log.levels.WARN)
            return
        end
        require('kodelet.context').send_selection()
    end, { range = true, desc = "Send selection to Kodelet" })
    
    -- Detach from session
    vim.api.nvim_create_user_command("KodeletDetach", function()
        writer.set_conversation_id(nil)
        vim.notify("Detached from Kodelet session", vim.log.levels.INFO)
    end, { desc = "Detach from Kodelet session" })
    
    -- Show status
    vim.api.nvim_create_user_command("KodeletStatus", function()
        if writer.conversation_id then
            vim.notify("Attached to Kodelet: " .. writer.conversation_id, vim.log.levels.INFO)
        else
            vim.notify("Not attached to Kodelet session", vim.log.levels.WARN)
        end
    end, { desc = "Show Kodelet connection status" })
    
    -- Clear context manually
    vim.api.nvim_create_user_command("KodeletClearContext", function()
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session", vim.log.levels.WARN)
            return
        end
        
        if writer.clear_context() then
            vim.notify("Context cleared", vim.log.levels.INFO)
        else
            vim.notify("No context to clear", vim.log.levels.INFO)
        end
    end, { desc = "Clear Kodelet context" })
    
    -- Clear only selection (keep files and diagnostics)
    vim.api.nvim_create_user_command("KodeletClearSelection", function()
        if not writer.conversation_id then
            vim.notify("Not attached to a Kodelet session", vim.log.levels.WARN)
            return
        end
        
        writer.clear_selection()
    end, { desc = "Clear Kodelet selection only" })
end

return M
