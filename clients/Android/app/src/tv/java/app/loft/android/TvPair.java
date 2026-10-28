/*
 * Android client, TV build: a tiny one-purpose HTTP server on the TV's LAN
 * address for phone-to-TV pairing. TvActivity shows its URL as a QR with a
 * short code; the phone (ScanActivity) POSTs its profiles there. URL shape
 * and the code check live in PairUrl, shared with the phone build.
 */
package app.loft.android;

import android.os.Handler;
import android.os.Looper;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Collections;
import java.util.function.Consumer;

/**
 * Short-lived LAN HTTP server that accepts POST /pair?c=CODE for phone → TV
 * import. Typing a long profile link on a TV remote is painful, so the TV
 * listens on the local network while its pairing screen is open and the
 * phone sends the profiles over. One instance serves one pairing session:
 * start() when the screen opens, stop() when it closes. The server runs on
 * its own background thread; only the received body is handed back to the
 * main thread.
 */
final class TvPair {
    // Code alphabet without 0/O and 1/I, which look alike on a TV screen.
    private static final char[] ALPHA = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ".toCharArray();

    /** Listening socket on a random free port; null while stopped. */
    private ServerSocket ss;
    /** Background thread running loop(); kept only for reference, stop() ends it by closing ss. */
    private Thread th;
    /** Cleared by stop() so the accept loop exits. */
    private volatile boolean run;
    /** Code shown on screen; a request must carry it to be accepted. */
    final String code = newCode();
    /** LAN address, port and full pairing URL; empty / 0 while stopped. */
    String host = "";
    int port;
    String url = "";

    /**
     * Opens a listener on a random port of the TV's LAN address and builds the
     * pairing URL for the QR. Each request body is handed to {@code onBody} on
     * the main thread. Returns false when the TV has no usable LAN address.
     */
    boolean start(Consumer<String> onBody) {
        stop();
        host = lanIPv4();
        if (host.isEmpty()) {
            return false;
        }
        try {
            ss = new ServerSocket(0);
            port = ss.getLocalPort();
            url = PairUrl.build(host, port, code);
            run = true;
            // The accept loop blocks, so it runs on a daemon thread (it never
            // keeps the process alive); results are posted back through a
            // main-thread Handler because onBody touches the UI.
            Handler h = new Handler(Looper.getMainLooper());
            th = new Thread(() -> loop(onBody, h), "loft-tv-pair");
            th.setDaemon(true);
            th.start();
            return true;
        } catch (Exception e) {
            Journal.add("pair " + e.getMessage());
            stop();
            return false;
        }
    }

    /** Closes the listener (which ends the accept loop) and clears the URL. Safe to call twice. */
    void stop() {
        run = false;
        try {
            if (ss != null) {
                ss.close();
            }
        } catch (Exception ignored) {
        }
        ss = null;
        th = null;
        url = "";
        port = 0;
    }

    /**
     * Serves one connection at a time until stop() closes the socket. Closing
     * the socket makes accept() throw; the error is only logged while run is
     * still true, so a normal stop leaves no noise in the journal.
     */
    private void loop(Consumer<String> onBody, Handler h) {
        while (run && ss != null && !ss.isClosed()) {
            try (Socket s = ss.accept()) {
                // A silent or slow client must not block the single serving thread forever.
                s.setSoTimeout(4000);
                handle(s, onBody, h);
            } catch (Exception e) {
                if (run) {
                    Journal.add("pair accept " + e.getMessage());
                }
            }
        }
    }

    /**
     * Minimal HTTP/1.1 handler: reads headers up to the blank line (8 KiB cap),
     * then a body of Content-Length bytes (256 KiB cap). Only POST /pair with
     * the code shown on screen is accepted: its body is the profiles text sent
     * by the phone and goes to {@code onBody}. Anything else, from any device
     * on the LAN, gets 404 and changes nothing; the code is never sent back.
     */
    private void handle(Socket s, Consumer<String> onBody, Handler h) throws Exception {
        // 1. Read the request line and headers byte by byte until "\r\n\r\n".
        InputStream in = s.getInputStream();
        ByteArrayOutputStream head = new ByteArrayOutputStream();
        int prev = 0, n;
        while ((n = in.read()) >= 0) {
            head.write(n);
            if (prev == '\n' && n == '\r') {
                int last = in.read();
                if (last == '\n') {
                    break;
                }
                if (last >= 0) {
                    head.write(last);
                }
            }
            prev = n;
            if (head.size() > 8192) {
                return;
            }
        }
        // 2. Find Content-Length and check the request line (method, path, code).
        String hdr = head.toString("ISO-8859-1");
        int len = 0;
        for (String line : hdr.split("\r\n")) {
            if (line.toLowerCase().startsWith("content-length:")) {
                try {
                    len = Integer.parseInt(line.substring(15).trim());
                } catch (Exception ignored) {
                }
            }
        }
        int eol = hdr.indexOf("\r\n");
        boolean ok = PairUrl.authorized(eol < 0 ? hdr : hdr.substring(0, eol), code);
        // 3. Read the body, capped at 256 KiB.
        byte[] body = new byte[Math.max(0, Math.min(len, 256 * 1024))];
        int got = 0;
        while (got < body.length) {
            int r = in.read(body, got, body.length - got);
            if (r < 0) {
                break;
            }
            got += r;
        }
        String text = new String(body, 0, got, StandardCharsets.UTF_8);
        // 4. Pass an accepted body to the UI thread and answer.
        OutputStream out = s.getOutputStream();
        if (ok) {
            if (!text.isEmpty()) {
                h.post(() -> onBody.accept(text));
            }
            reply(out, 200, "ok\n");
        } else {
            reply(out, 404, "not found\n");
        }
    }

    /** Writes a tiny plain-text HTTP response (200 or 404) with "Connection: close". */
    private static void reply(OutputStream out, int code, String body) throws Exception {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        String h = "HTTP/1.1 " + code + (code == 200 ? " OK" : " Not Found") + "\r\n"
                + "Content-Type: text/plain; charset=utf-8\r\n"
                + "Content-Length: " + b.length + "\r\n"
                + "Connection: close\r\n\r\n";
        out.write(h.getBytes(StandardCharsets.US_ASCII));
        out.write(b);
        out.flush();
    }

    /**
     * First non-loopback, non-link-local IPv4 address of an interface that is
     * up. Addresses in 10.8.x.x are skipped: that is the common loft overlay
     * range (for example 10.8.0.0/24), which the phone on the LAN cannot reach.
     */
    static String lanIPv4() {
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp() || ni.isLoopback()) {
                    continue;
                }
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    if (!(a instanceof Inet4Address) || a.isLoopbackAddress() || a.isLinkLocalAddress()) {
                        continue;
                    }
                    byte[] b = a.getAddress();
                    if (b.length == 4 && b[0] == 10 && b[1] == 8) {
                        continue;
                    }
                    return a.getHostAddress();
                }
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    /**
     * Random 6-character pairing code shown next to the QR. SecureRandom is
     * used because the code is the only thing stopping another device on the
     * same LAN from pushing profiles to the TV.
     */
    static String newCode() {
        SecureRandom r = new SecureRandom();
        char[] c = new char[6];
        for (int i = 0; i < c.length; i++) {
            c[i] = ALPHA[r.nextInt(ALPHA.length)];
        }
        return new String(c);
    }
}
