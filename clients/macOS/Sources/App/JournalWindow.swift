// Journal window: front and session log lines, copy/export/clear, rotating diag file path.
// The journal is the in-app event log kept by Engine/LoftLog.swift. With the
// diagnostics option on, the same lines also go to a log file on disk.

import AppKit

/// Copy selection, or the whole log when nothing is selected.
final class JournalText: NSTextView {
    /// Copies the selection, or the whole log when the selection is empty.
    override func copy(_ sender: Any?) {
        if selectedRange().length == 0 {
            let s = string
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(s, forType: .string)
            return
        }
        super.copy(sender)
    }

    /// Glyphs for 0..<storage.length. DrawRect still fills holes if this is skipped.
    private func fillGlyphs() {
        guard let lm = layoutManager, let st = textStorage else { return }
        let n = st.length
        if n > 0 {
            lm.ensureLayout(forCharacterRange: NSRange(location: 0, length: n))
        }
    }

    /// Layout before CA display. NSException from hole-fill is caught; Swift cannot.
    override func viewWillDraw() {
        _ = LoftCatch { [unowned self] in self.fillGlyphs() }
        super.viewWillDraw()
    }

    /// NSTextView.drawRect uses okToFillHoles:YES. Inconsistent glyphs →
    /// _replaceElements NSException → AppKit _crashOnException SIGILL.
    override func draw(_ dirtyRect: NSRect) {
        let r = dirtyRect
        _ = LoftCatch { [unowned self] in self.drawBody(r) }
    }

    /// Super draw; called only from LoftCatch so NSException stays in ObjC.
    private func drawBody(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
    }
}

/// Journal window: front and session log lines, copy/export/clear, rotating diag file path.
final class JournalWindowController: NSWindowController, NSWindowDelegate, NSTextViewDelegate {
    private let titleLab = NSTextField(labelWithString: "")
    private let sub = NSTextField(wrappingLabelWithString: "")
    private let scroll = NSScrollView(frame: .zero)
    private let text = JournalText(frame: .zero)
    private let copyBtn = GhostButton(frame: .zero)
    private let exportBtn = GhostButton(frame: .zero)
    private let clearBtn = GhostButton(frame: .zero)
    private let back = GhostButton(frame: .zero)
    private var painted = ""
    /// True while reload() mutates NSTextStorage. textViewDidChangeSelection
    /// would otherwise recurse into reload during the replace.
    private var painting = false

    /// Builds the journal window and subscribes to log/state notifications.
    init() {
        let w = makeLoftWindow(size: NSSize(width: 520, height: 480))
        super.init(window: w)
        w.level = .normal
        w.delegate = self
        build()
        w.center()
        NotificationCenter.default.addObserver(self, selector: #selector(reload), name: .loftLog, object: nil)
        reload()
    }

    /// Storyboard path is unused.
    required init?(coder: NSCoder) { fatalError() }

    /// Puts the log view first so copy/select works as soon as the window is key.
    func windowDidBecomeKey(_ notification: Notification) {
        window?.makeFirstResponder(text)
    }

    /// Reloads when a selection is cleared so a held update can land.
    func textViewDidChangeSelection(_ notification: Notification) {
        guard !painting, text.selectedRange().length == 0 else { return }
        let body = LoftLog.shared.dump()
        let shown = body.isEmpty ? L10n.t("journal_empty") : body
        if shown != painted { reload() }
    }

    /// Lays out title, log scroll, and copy/export/clear/back buttons.
    private func build() {
        let c = loftBody(window!)
        titleLab.font = Palette.font(20)
        titleLab.textColor = Palette.snow
        titleLab.alignment = .center
        sub.font = Palette.body(12)
        sub.textColor = Palette.mute
        sub.alignment = .center

        // Rounded log area.
        scroll.drawsBackground = true
        scroll.backgroundColor = Palette.field
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        scroll.wantsLayer = true
        scroll.layer?.backgroundColor = Palette.field.cgColor
        scroll.layer?.cornerRadius = 12
        scroll.layer?.masksToBounds = true
        scroll.contentView.drawsBackground = true
        scroll.contentView.backgroundColor = Palette.field

        // Read-only, selectable monospace text with no automatic "smart" replacements.
        text.isEditable = false
        text.isSelectable = true
        text.drawsBackground = false
        text.backgroundColor = .clear
        text.textColor = Palette.snow
        text.font = Palette.mono(11)
        text.isRichText = false
        text.isVerticallyResizable = true
        text.isHorizontallyResizable = false
        text.textContainerInset = NSSize(width: 10, height: 10)
        text.textContainer?.widthTracksTextView = true
        text.textContainer?.containerSize = NSSize(width: 460, height: CGFloat.greatestFiniteMagnitude)
        text.isAutomaticQuoteSubstitutionEnabled = false
        text.isAutomaticDashSubstitutionEnabled = false
        text.isAutomaticTextReplacementEnabled = false
        text.enabledTextCheckingTypes = 0
        text.delegate = self
        // Layer-backed NSTextView drawRect fills holes (okToFillHoles) on CA
        // commit. Dist 1.0.46 died SIGILL there during GET hold/reset spam.
        text.wantsLayer = false
        // Holes from a longer previous string survive text.string=; ensureLayout
        // then walks them and NSString _getBlockStart: raises → SIGSEGV (Clear).
        text.layoutManager?.allowsNonContiguousLayout = false
        applyLogStyle()
        scroll.documentView = text

        // Footer: Copy · Export · Clear · Back, space-between in 520.
        copyBtn.font = Palette.body(12.5)
        copyBtn.tone = Palette.mute
        copyBtn.alignment = .left
        copyBtn.refusesFirstResponder = true
        copyBtn.target = self
        copyBtn.action = #selector(copyTap)

        exportBtn.font = Palette.body(12.5)
        exportBtn.tone = Palette.mute
        exportBtn.alignment = .center
        exportBtn.refusesFirstResponder = true
        exportBtn.target = self
        exportBtn.action = #selector(exportTap)

        clearBtn.font = Palette.body(12.5)
        clearBtn.tone = Palette.mute
        clearBtn.alignment = .center
        clearBtn.refusesFirstResponder = true
        clearBtn.target = self
        clearBtn.action = #selector(clearTap)

        back.font = Palette.body(12.5)
        back.tone = Palette.mute
        back.alignment = .right
        back.refusesFirstResponder = true
        back.target = self
        back.action = #selector(backTap)

        for v in [titleLab, sub, scroll, copyBtn, exportBtn, clearBtn, back] as [NSView] {
            c.addSubview(v)
        }
        layout()
    }

    /// Tab stops for time, source, and wrapping message columns.
    private func logStyle() -> NSParagraphStyle {
        let font = Palette.mono(11)
        let gap: CGFloat = 12
        let t0 = Palette.textWidth("00:00:00", font: font) + gap
        let t1 = t0 + Palette.textWidth("front", font: font) + gap
        let p = NSMutableParagraphStyle()
        p.tabStops = [
            NSTextTab(textAlignment: .left, location: t0, options: [:]),
            NSTextTab(textAlignment: .left, location: t1, options: [:])
        ]
        p.firstLineHeadIndent = 0
        p.headIndent = t1
        p.lineBreakMode = .byCharWrapping
        p.lineSpacing = 1
        return p
    }

    /// Applies mono font and column tabs to the text view.
    private func applyLogStyle() {
        let p = logStyle()
        text.defaultParagraphStyle = p
        var attrs = text.typingAttributes
        attrs[.font] = Palette.mono(11)
        attrs[.paragraphStyle] = p
        text.typingAttributes = attrs
    }

    /// Positions title, scroll, and footer buttons; resizes the window.
    private func layout() {
        let w: CGFloat = 520
        let bodyH: CGFloat = 452
        let btnH: CGFloat = 22
        let bottom: CGFloat = 18
        let gap: CGFloat = 14
        titleLab.frame = NSRect(x: 20, y: 8, width: w - 40, height: 26)
        sub.frame = NSRect(x: 28, y: 36, width: w - 56, height: 32)
        let btnY = bodyH - bottom - btnH
        let scrollY: CGFloat = 76
        let scrollH = btnY - gap - scrollY
        scroll.frame = NSRect(x: 20, y: scrollY, width: w - 40, height: scrollH)
        let inner = w - 40 - 20
        text.frame = NSRect(x: 0, y: 0, width: inner, height: scrollH)
        text.textContainer?.containerSize = NSSize(width: inner, height: CGFloat.greatestFiniteMagnitude)
        let btnW: CGFloat = 110
        let span = (w - 40) - btnW
        copyBtn.frame = NSRect(x: 20, y: btnY, width: btnW, height: btnH)
        exportBtn.frame = NSRect(x: 20 + span / 3, y: btnY, width: btnW, height: btnH)
        clearBtn.frame = NSRect(x: 20 + 2 * span / 3, y: btnY, width: btnW, height: btnH)
        back.frame = NSRect(x: 20 + span, y: btnY, width: btnW, height: btnH)
        if let win = window {
            loftResize(win, bodyHeight: bodyH)
        }
    }

    /// Refreshes captions and the log body; skips redraw while the user has a selection.
    @objc func reload() {
        titleLab.stringValue = L10n.t("journal")
        if LoftLog.shared.diag {
            sub.stringValue = L10n.t("journal_diag") + "\n" + LoftLog.shared.fileURL.path
        } else {
            sub.stringValue = L10n.t("journal_d")
        }
        copyBtn.title = L10n.t("copy")
        exportBtn.title = L10n.t("export")
        clearBtn.title = L10n.t("clear_log")
        back.title = L10n.t("back")
        // Skip the redraw when hidden, unchanged, or while the user has a selection
        // (replacing the text would lose it).
        guard window?.isVisible == true else { return }
        let body = LoftLog.shared.dump()
        let shown = body.isEmpty ? L10n.t("journal_empty") : body
        if shown == painted { return }
        if text.selectedRange().length > 0 { return }
        guard !painting else { return }
        guard let storage = text.textStorage else { return }
        painting = true
        defer { painting = false }
        let old = painted
        painted = shown
        applyLogStyle()
        let inner = scroll.bounds.width > 1 ? scroll.bounds.width : 480
        let width = max(40, inner - 20)
        text.textContainer?.containerSize = NSSize(width: width, height: CGFloat.greatestFiniteMagnitude)
        let empty = L10n.t("journal_empty")
        let style = logStyle()
        let font = Palette.mono(11)
        let color: NSColor = body.isEmpty ? Palette.mute : Palette.snow
        let attrs: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: color,
            .paragraphStyle: style,
        ]
        // New lines only appended: add just the tail. Anything else: replace all
        // in one processEditing so leftover glyph holes are dropped.
        let mutated = LoftCatch {
            storage.beginEditing()
            if shown.hasPrefix(old), !old.isEmpty, old != empty, !body.isEmpty {
                let extra = String(shown.dropFirst(old.count))
                if !extra.isEmpty {
                    storage.append(NSAttributedString(string: extra, attributes: attrs))
                }
            } else {
                storage.setAttributedString(NSAttributedString(string: shown, attributes: attrs))
            }
            storage.endEditing()
        }
        if !mutated {
            painted = old
            return
        }
        text.defaultParagraphStyle = style
        text.font = font
        text.textColor = color
        fitLog(inner)
        if !body.isEmpty {
            let n = storage.length
            text.scrollRangeToVisible(NSRange(location: max(0, n - 1), length: 0))
        }
    }

    /// Sizes the text view from glyphs of the live character range.
    /// ensureLayout(for: container) fills holes past storage.length after a
    /// long→short replace (Clear); NSString _getBlockStart: raises, Swift
    /// does not catch NSException, loft dies SIGSEGV on the main thread.
    private func fitLog(_ inner: CGFloat) {
        guard let storage = text.textStorage, let lm = text.layoutManager, let tc = text.textContainer else { return }
        let n = storage.length
        if n > 0 {
            var used = NSRect.zero
            let laid = LoftCatch {
                lm.ensureLayout(forCharacterRange: NSRange(location: 0, length: n))
                used = lm.usedRect(for: tc)
            }
            let h = laid ? max(scroll.bounds.height, used.height + 24) : max(scroll.bounds.height, 1)
            text.frame = NSRect(x: 0, y: 0, width: inner, height: h)
        } else {
            text.frame = NSRect(x: 0, y: 0, width: inner, height: max(scroll.bounds.height, 1))
        }
    }

    /// Window copy action: dumps the journal to the pasteboard.
    @objc func copy(_ sender: Any?) { copyTap() }

    /// Copies the journal (or the empty placeholder) to the pasteboard.
    @objc private func copyTap() {
        let s = LoftLog.shared.dump()
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(s.isEmpty ? L10n.t("journal_empty") : s, forType: .string)
    }

    /// NSSavePanel of dump() as UTF-8 .txt. LoftLog.redact already ran on each line.
    @objc private func exportTap() {
        let p = NSSavePanel()
        p.allowedFileTypes = ["txt"]
        p.canCreateDirectories = true
        p.isExtensionHidden = false
        p.nameFieldStringValue = "loft-journal.txt"
        let go: (NSApplication.ModalResponse) -> Void = { r in
            guard r == .OK, let u = p.url else { return }
            var s = LoftLog.shared.dump()
            if !s.isEmpty, !s.hasSuffix("\n") { s.append("\n") }
            try? s.write(to: u, atomically: true, encoding: .utf8)
        }
        if let w = window {
            p.beginSheetModal(for: w, completionHandler: go)
        } else {
            p.begin(completionHandler: go)
        }
    }

    /// Clears the in-memory journal and the diag file if diag is on.
    @objc private func clearTap() { LoftLog.shared.clear() }
    /// Closes the journal window.
    @objc private func backTap() { window?.close() }
}
