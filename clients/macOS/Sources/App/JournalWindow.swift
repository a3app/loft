// Journal window: front and session log lines, copy/clear, rotating diag file path.
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
}

/// Journal window: front and session log lines, copy/clear, rotating diag file path.
final class JournalWindowController: NSWindowController, NSWindowDelegate, NSTextViewDelegate {
    private let titleLab = NSTextField(labelWithString: "")
    private let sub = NSTextField(wrappingLabelWithString: "")
    private let scroll = NSScrollView(frame: .zero)
    private let text = JournalText(frame: .zero)
    private let copyBtn = GhostButton(frame: .zero)
    private let clearBtn = GhostButton(frame: .zero)
    private let back = GhostButton(frame: .zero)
    private var painted = ""

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
        guard text.selectedRange().length == 0 else { return }
        let body = LoftLog.shared.dump()
        let shown = body.isEmpty ? L10n.t("journal_empty") : body
        if shown != painted { reload() }
    }

    /// Lays out title, log scroll, and copy/clear/back buttons.
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
        applyLogStyle()
        scroll.documentView = text

        // Footer: Copy on the left, Clear in the middle, Back on the right.
        copyBtn.font = Palette.body(12.5)
        copyBtn.tone = Palette.mute
        copyBtn.alignment = .left
        copyBtn.refusesFirstResponder = true
        copyBtn.target = self
        copyBtn.action = #selector(copyTap)

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

        for v in [titleLab, sub, scroll, copyBtn, clearBtn, back] as [NSView] {
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
        copyBtn.frame = NSRect(x: 20, y: btnY, width: 140, height: btnH)
        clearBtn.frame = NSRect(x: 190, y: btnY, width: 140, height: btnH)
        back.frame = NSRect(x: 360, y: btnY, width: 140, height: btnH)
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
        clearBtn.title = L10n.t("clear_log")
        back.title = L10n.t("back")
        // Skip the redraw when hidden, unchanged, or while the user has a selection
        // (replacing the text would lose it).
        guard window?.isVisible == true else { return }
        let body = LoftLog.shared.dump()
        let shown = body.isEmpty ? L10n.t("journal_empty") : body
        if shown == painted { return }
        if text.selectedRange().length > 0 { return }
        let old = painted
        painted = shown
        applyLogStyle()
        let inner = scroll.bounds.width > 1 ? scroll.bounds.width : 480
        text.textContainer?.containerSize = NSSize(width: max(40, inner - 20), height: CGFloat.greatestFiniteMagnitude)
        let empty = L10n.t("journal_empty")
        // New lines only appended: add just the tail. Anything else: replace all.
        if shown.hasPrefix(old), !old.isEmpty, old != empty, let storage = text.textStorage, !body.isEmpty {
            let extra = String(shown.dropFirst(old.count))
            storage.beginEditing()
            storage.append(NSAttributedString(string: extra, attributes: [
                .font: Palette.mono(11),
                .foregroundColor: Palette.snow,
                .paragraphStyle: logStyle(),
            ]))
            storage.endEditing()
        } else {
            text.string = shown
            text.textColor = body.isEmpty ? Palette.mute : Palette.snow
            text.font = Palette.mono(11)
            let style = logStyle()
            text.defaultParagraphStyle = style
            let n = (text.string as NSString).length
            if n > 0 {
                let r = NSRange(location: 0, length: n)
                text.textStorage?.addAttribute(.paragraphStyle, value: style, range: r)
                text.textStorage?.addAttribute(.font, value: Palette.mono(11), range: r)
            }
        }
        // Size the text view to its content and scroll to the newest line.
        text.layoutManager?.ensureLayout(for: text.textContainer!)
        let used = text.layoutManager?.usedRect(for: text.textContainer!) ?? .zero
        let h = max(scroll.bounds.height, used.height + 24)
        text.frame = NSRect(x: 0, y: 0, width: inner, height: h)
        if !body.isEmpty {
            let n = (text.string as NSString).length
            text.scrollRangeToVisible(NSRange(location: max(0, n - 1), length: 0))
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

    /// Clears the in-memory journal and the diag file if diag is on.
    @objc private func clearTap() { LoftLog.shared.clear() }
    /// Closes the journal window.
    @objc private func backTap() { window?.close() }
}
