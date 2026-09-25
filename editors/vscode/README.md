# Cicada for VS Code

Open a `.cicada` score for diagnostics, hovers, inlay hints, semantic color,
rename, and go-to-definition. Run **Cicada: Open Studio Beside Score** for the
live Code, Session, History, and Voice views next to the notation.

Build the Cicada binary with `go build -o cicada ./cmd/cicada` from the repository
root. Put it on `PATH`, or set `cicada.serverPath` to its absolute path. Run
`npm install` in this folder, then launch an Extension Development Host with
`code --extensionDevelopmentPath=/path/to/editors/vscode`. When working in WSL,
run the extension in the WSL extension host and point it at the Linux Cicada
binary.

To install a packaged copy, run `npx @vscode/vsce package` in this folder and
then `code --install-extension cicada-0.1.0.vsix` in the same environment as the
Cicada binary.

The extension starts `cicada studio score.cicada --lsp-stdio`. One process speaks
LSP on stdio and serves Studio on a loopback port. Studio edits the score file;
VS Code detects the file change and updates the text buffer. Saving notation in
VS Code updates Studio on its next refresh. The Studio process exits with the
language client.
