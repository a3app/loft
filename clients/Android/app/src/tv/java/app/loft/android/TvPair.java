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

/** Short-lived LAN HTTP POST /pair?c=CODE for phone → TV import. */
final class TvPair {
    // Code alphabet without 0/O and 1/I, which look alike on a TV screen.
    private static final char[] ALPHA = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ".toCharArray();

    private ServerSocket ss;
    private Thread th;
    private volatile boolean run;
    final String code = newCode();
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

    // Serves one connection at a time until stop() closes the socket.
    private void loop(Consumer<String> onBody, Handler h) {
        while (run && ss != null && !ss.isClosed()) {
            try (Socket s = ss.accept()) {
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
     * then a body of Content-Length bytes (256 KiB cap). A POST body is the
     * profiles text sent by the phone and goes to {@code onBody}; any other
     * request gets the pairing code back.
     */
    private void handle(Socket s, Consumer<String> onBody, Handler h) throws Exception {
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
        boolean post = hdr.startsWith("POST ");
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
        OutputStream out = s.getOutputStream();
        if (post) {
            if (!text.isEmpty()) {
                h.post(() -> onBody.accept(text));
            }
            reply(out, 200, "ok\n");
        } else {
            reply(out, 200, code + "\n");
        }
    }

    private static void reply(OutputStream out, int code, String body) throws Exception {
        byte[] b = body.getBytes(StandardCharsets.UTF_8);
        String h = "HTTP/1.1 " + code + " OK\r\n"
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

    /** Random 6-character pairing code shown next to the QR. */
    static String newCode() {
        SecureRandom r = new SecureRandom();
        char[] c = new char[6];
        for (int i = 0; i < c.length; i++) {
            c[i] = ALPHA[r.nextInt(ALPHA.length)];
        }
        return new String(c);
    }
}
