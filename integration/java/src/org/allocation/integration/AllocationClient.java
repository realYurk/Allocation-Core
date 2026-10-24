package org.allocation.integration;

import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.nio.charset.StandardCharsets;
import java.util.regex.Pattern;
import java.util.Set;
import java.util.Collections;
import java.util.HashSet;
import java.util.Arrays;

/** Lightweight Java 8 HTTP adapter. NEVER computes allocations itself. */
public final class AllocationClient {
    private static final Pattern ID = Pattern.compile("[A-Za-z0-9_-]{1,128}");
    public static final String SDK_VERSION = "21.1.0";
    /** Exactly the ten downloadable filenames advertised by the frozen V20 service. */
    private static final Set<String> ALLOWED_FILES = Collections.unmodifiableSet(new HashSet<String>(Arrays.asList(
        "results.csv", "ledger.csv", "ledger.csv.gz", "reconciliation.csv", "source_to_pool.csv",
        "source_to_pool.csv.gz", "lineage_links.csv", "lineage_links.csv.gz",
        "evidence_stream_manifest.json", "run_summary.json")));
    private final String baseUrl;
    private final String token;

    public AllocationClient(String baseUrl, String bearerToken) {
        if (!(baseUrl.startsWith("http://127.0.0.1:") || baseUrl.startsWith("http://localhost:") || baseUrl.startsWith("https://"))) {
            throw new IllegalArgumentException("Use local loopback or HTTPS with an approved gateway");
        }
        this.baseUrl = baseUrl.endsWith("/") ? baseUrl.substring(0,baseUrl.length()-1) : baseUrl;
        this.token = bearerToken == null ? "" : bearerToken;
    }
    private static String escapeJson(String text) {
        return text.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t");
    }
    private HttpURLConnection connect(String route, String method, String contentType) throws IOException {
        HttpURLConnection connection = (HttpURLConnection)new URL(baseUrl + route).openConnection();
        connection.setRequestMethod(method);
        connection.setConnectTimeout(5000);
        connection.setReadTimeout(60000);
        connection.setRequestProperty("Accept", contentType);
        if (!token.isEmpty()) connection.setRequestProperty("Authorization", "Bearer " + token);
        return connection;
    }
    private static void requireSuccess(HttpURLConnection c) throws IOException {
        int code = c.getResponseCode();
        if (code < 200 || code >= 300) {
            String text = "";
            try (InputStream stream = c.getErrorStream()) {
                if (stream != null) text = readAll(stream);
            }
            throw new IOException("Allocation HTTP error " + code + " " + text);
        }
    }
    private static String readAll(InputStream stream) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[8192]; int n;
        while ((n = stream.read(buf)) != -1) out.write(buf,0,n);
        return new String(out.toByteArray(), StandardCharsets.UTF_8);
    }
    public String submit(String relativeRule) throws IOException {
        if (relativeRule == null || relativeRule.isEmpty() || relativeRule.startsWith("/") || relativeRule.contains("..") || relativeRule.contains("\\"))
            throw new IllegalArgumentException("Rule must be a relative path registered under server --root");
        String payload = "{\"rule\":\""+escapeJson(relativeRule)+"\",\"workers\":2,\"disk_partitions\":4,\"evidence_gzip\":true}";
        HttpURLConnection c = connect("/v1/jobs", "POST", "application/json");
        c.setRequestProperty("Content-Type","application/json; charset=utf-8");
        c.setDoOutput(true);
        try {
            try (OutputStream out=c.getOutputStream()) {out.write(payload.getBytes(StandardCharsets.UTF_8));}
            requireSuccess(c);
            try (InputStream in=c.getInputStream()) {return readAll(in);}
        } finally { c.disconnect(); }
    }
    public String status(String jobId) throws IOException {
        if (!ID.matcher(jobId).matches()) throw new IllegalArgumentException("Invalid jobId");
        HttpURLConnection c = connect("/v1/jobs/"+jobId,"GET","application/json");
        try {requireSuccess(c); try (InputStream in=c.getInputStream()) {return readAll(in);}}
        finally {c.disconnect();}
    }
    /** Download binary CSV without loading millions of rows into Java heap. */
    public void download(String jobId, String filename, Path target) throws IOException {
        if (!ID.matcher(jobId).matches()) throw new IllegalArgumentException("Invalid jobId");
        if (!ALLOWED_FILES.contains(filename))
            throw new IllegalArgumentException("Unsupported endpoint filename");
        if (target == null) throw new IllegalArgumentException("target is required");
        HttpURLConnection c=connect("/v1/jobs/"+jobId+"/"+filename,"GET","application/octet-stream");
        Path destination = target.toAbsolutePath();
        Path parent = destination.getParent();
        Files.createDirectories(parent);
        Path temporary = null;
        try {
            requireSuccess(c);
            // On HTTP failure or interrupted transfer, never replace a previously valid file.
            temporary = Files.createTempFile(parent, ".allocation-download-", ".part");
            long expectedBytes = c.getContentLengthLong();
            long actualBytes;
            try (InputStream in=c.getInputStream()) {
                actualBytes = Files.copy(in, temporary, StandardCopyOption.REPLACE_EXISTING);
            }
            if (expectedBytes >= 0 && actualBytes != expectedBytes)
                throw new IOException("Truncated download: expected=" + expectedBytes + " actual=" + actualBytes);
            try {
                Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (java.nio.file.AtomicMoveNotSupportedException ignored) {
                Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING);
            }
        } finally {
            if (temporary != null) Files.deleteIfExists(temporary);
            c.disconnect();
        }
    }
    public String health() throws IOException {
        HttpURLConnection c=connect("/healthz","GET","application/json");
        try {requireSuccess(c); try (InputStream in=c.getInputStream()) {return readAll(in);}}
        finally {c.disconnect();}
    }
}
