# Cicada for VS Code

Open a `.cicada` score for diagnostics, hovers, inlay hints, semantic color,
rename, go-to-definition, and **Apply Cicada notation fixes**. Run **Cicada:
Open Studio Beside Score** to open Studio next to the notation. Studio's panels
include Session, Patterns, Live, Mixer, Score, and History; the Studio chapter
of the user manual describes them.

Build the Cicada binary and the Studio workstation with `make build` from the
repository root. The extension runs `cicada studio`, which looks for
`cicada-workstation` next to `cicada` and then on `PATH`. Set `cicada.serverPath`
to the absolute path of `build/cicada`, or move `cicada`, `cicada-workstation`,
and the `workstation` folder together into a folder on `PATH`. Run `npm install`
in this folder, then launch an Extension Development Host with
`code --extensionDevelopmentPath=/path/to/editors/vscode`. When working in WSL,
run the extension in the WSL extension host and point it at the Linux Cicada
binary.

To install a packaged copy, run `npx @vscode/vsce package` in this folder and
then `code --install-extension cicada-0.1.0.vsix` in the same environment as the
Cicada binary.

The extension starts `cicada studio score.cicada --lsp-stdio`. That process
speaks LSP on stdio and starts the workstation, which serves Studio on a
loopback port. Studio edits the score file; VS Code detects the file change and
updates the text buffer. Saving notation in VS Code updates Studio on its next
poll. An open Studio draft stays in the frame. If the score has errors at
startup, Studio waits for a valid save before it opens the score view. The
Studio process exits with the language client.
