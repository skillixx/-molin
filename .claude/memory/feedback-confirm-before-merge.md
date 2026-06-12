---
name: feedback-confirm-before-merge
description: 涉及合并 PR 到 main 的操作（派 产品经理 review+merge）必须先和用户确认，不能因为本轮会话里之前的 PR 已被批准合并就自动延续
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 702abc53-b89e-464e-bf60-d1ca4200e2ec
---

任何会把 PR 合并到 main 的操作，都必须先向用户确认，每个 PR 单独确认，不能因为同一会话里前面的 PR 已经被用户明确要求"审查合并"过，就推断后续新产生的 PR 也默认可以自动走"创建 PR → 派 产品经理 review+merge"的完整流程。

**Why:** 合并到 main 是影响共享状态、难以撤销的操作。之前几次（PR#12~16）合并都是用户明确说"审查合并 PRxx"。但在 PR#17（README 更新）这次，用户只要求"更新 README"，我自行把任务范围扩大到"创建 PR 后派产品经理审查并合并"，产品经理 agent 因此自主执行了 `gh pr merge --merge` 触发了安全警告。用户随后明确要求：以后涉及合并到 main 都先跟我确认。

**How to apply:**
- 完成一项任务后如果产出是一个新 PR，正常情况下应该停在"PR 已创建，链接是 xxx"这一步，向用户报告并询问是否需要审查合并。
- 只有用户在本轮请求中明确说了"审查合并"、"合并"、"merge"等字眼，或者通过 AskUserQuestion 确认后，才可以派 产品经理（或其他 agent）执行 `gh pr merge`。
- 派给 产品经理 agent 的 prompt 里，如果任务本身只是"review"，要明确告知 agent **不要自行执行合并**，仅输出审查结论，等待用户确认后再由我安排合并。
- 部署测试服务器等后续步骤同理：只有 main 上已经合并的内容才能部署，因此"是否合并"这个确认点会连带影响后续部署节奏。

关联：[[feedback-git-workflow]]（开发者侧的分支规范）。
