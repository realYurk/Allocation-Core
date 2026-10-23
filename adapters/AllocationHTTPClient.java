import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;

/** Java 8 standard-library HTTP adapter. No allocation calculation is duplicated. */
public final class AllocationHTTPClient {
    private static String execute(String url, String method, String json, String bearer) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setRequestMethod(method);
        c.setConnectTimeout(10000);
        c.setReadTimeout(60000);
        c.setRequestProperty("Accept", "application/json");
        if (bearer != null && !bearer.isEmpty()) c.setRequestProperty("Authorization", "Bearer " + bearer);
        if (json != null) {
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json; charset=UTF-8");
            try (OutputStream out = c.getOutputStream()) {
                out.write(json.getBytes(StandardCharsets.UTF_8));
            }
        }
        int code = c.getResponseCode();
        InputStream stream = code >= 400 ? c.getErrorStream() : c.getInputStream();
        if (stream == null) throw new IOException("HTTP " + code);
        try (InputStream in = stream; ByteArrayOutputStream buf = new ByteArrayOutputStream()) {
            byte[] bytes = new byte[8192]; int n;
            while ((n = in.read(bytes)) != -1) buf.write(bytes, 0, n);
            String response = new String(buf.toByteArray(), StandardCharsets.UTF_8);
            if (code >= 400) throw new IOException("HTTP " + code + ": " + response);
            return response;
        } finally { c.disconnect(); }
    }
    public static String submit(String base, String rule, String bearer) throws IOException {
        // Keep user-supplied rule names validated by upstream JSON encoding in real systems.
        // The demo uses a fixed, safe file path registered under the server root.
        String body = "{\"rule\":\"" + rule + "\",\"workers\":2,\"disk_partitions\":8,\"driver_disk_index\":true}";
        return execute(base + "/v1/jobs", "POST", body, bearer);
    }
    public static void main(String[] args) throws Exception {
        String base = args.length > 0 ? args[0] : "http://127.0.0.1:8787";
        String rule = "manufacturing/rule.json";
        System.out.println("Health: " + execute(base + "/healthz", "GET", null, ""));
        System.out.println("Submitted: " + submit(base, rule, args.length > 1 ? args[1] : ""));
        System.out.println("GET " + base + "/v1/jobs to check status; results link is returned by ID.");
    }
}
