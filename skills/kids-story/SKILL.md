---
name: kids-story
description: Tell a children's story of the kind asked for, such as a bedtime, funny, adventure, spooky, fairy tale, animal or space story, or one with a lesson.
---
Call the kids-story tool with the user's request in a few words in args,
keeping the kind of story, its subject, any names, the child's age and
"short" or "long", such as ["funny story about a pirate cat"],
["short bedtime story for Mia"] or ["story about sharing for a 5 year old"].
For "another one", call it again with the same kind of story.

The tool returns a story brief: the kind of story and how to tell it, ideas
for anything the request leaves open, and its length. The tool doesn't tell
the story; you do. Right after it returns, tell the whole story as the brief
says. A story is the one reply that may be longer than three sentences.

If the tool fails, tell a short, gentle story of the kind asked for anyway.
