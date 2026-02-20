# Copilot In QA Tests

## Using Copilot through Github UI

### Overview

* create an issue with detailed instructions
* assign copilot to the issue
* follow the PR copilot creates on your behalf

### Creating an Issue for Copilot

Think of this as writing a test plan for a junior engineer. It should be fairly detailed, give examples, and explain vague or 'word of mouth' knowledge.
If there's a schema file for your test already, this is a great starting point.
Once you're done writing up the issue, simply assign copilot to the issue. You will be asked about adding additional instructions and selecting a model, but just use the defaults.

### Reviewing the PR created by Copilot

Initial review should be by the person who assigned copilot to the issue. This review should be treated just like any other junior engineer's PR; very thourough. This step is very important as the review burden in copilot PRs are on the person working with copilot, not the rest of the team. After you have had a deep review of the code and are as comfortable with it as though you'd written it yourself, then you may assign it to team members for a final approval.

### Caveats

* Interoperability helper functions may not work very well with this tool yet. We need to add some better instructions for copilot to know when / how to look up other APIs from other repos
* Sometimes, things you'd expect copilot to get right (i.e. changes it makes shouldn't break go builds) are not right. When this happens, please take note of what happened and create a separate issue/PR for it by adding some lines to `.github/copilot-instructions.md`
* Copilot currently has no way of knowing if the test passes or fails in a real environment. Currently, this is the biggest slowdown to copilot PRs, as we (test engineers) have to run it manually, then report the result back to copilot
