---
name: Personal Introduction
description: A recipe to generate a personal introduction based on name and occupation
allowed_tools: "thinking,file_write"
allowed_commands: []
arguments:
  name:
    description: Name to use in the introduction
  occupation:
    description: Occupation or role to use in the introduction
---

My name is {{.name}}, a {{.occupation}}.

Please write a short introduction about me.

Also tell me what tools you can use.
