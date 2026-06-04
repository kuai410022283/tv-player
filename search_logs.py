import os
import json

brain_dir = r"C:\Users\kuai\.gemini\antigravity-ide\brain"
for root, dirs, files in os.walk(brain_dir):
    for file in files:
        if file == "transcript.jsonl":
            filepath = os.path.join(root, file)
            try:
                with open(filepath, 'r', encoding='utf-8') as f:
                    for line in f:
                        if "线路复用" in line:
                            data = json.loads(line)
                            # the agent response might be in "content" or inside tool calls
                            if "content" in data and "线路复用" in data["content"] and "USER_INPUT" not in data.get("type", ""):
                                print("--- Found Agent Response in:", filepath, "---")
                                print(data["content"][:3000])
                                print("="*50)
            except Exception as e:
                pass
