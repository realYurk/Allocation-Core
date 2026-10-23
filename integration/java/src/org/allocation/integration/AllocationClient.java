package org.allocation.integration;

import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.nio.charset.StandardCharsets;
import java.util.regex.Pattern;

/** Lightweight Java 8 HTTP adapter. NEVER computes allocations itself. */
public final class AllocationClient {
    private static final Pattern ID = Pattern.compile("[A-Za-z0-9_-]{1,128}");
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
        if (!(filename.equals("results.csv") || filename.equals("reconciliation.csv") || filename.equals("ledger.csv") || filename.equals("source_to_pool.csv")))
            throw new IllegalArgumentException("Unsupported endpoint filename");
        HttpURLConnection c=connect("/v1/jobs/"+jobId+"/"+filename,"GET","text/csv");
        Path parent = target.toAbsolutePath().getParent();
        Files.createDirectories(parent);
        try {
            requireSuccess(c);
            try (InputStream in=c.getInputStream()) {Files.copy(in,target,StandardCopyOption.REPLACE_EXISTING);}
        } finally {c.disconnect();}
    }
    public String health() throws IOException {
        HttpURLConnection c=connect("/healthz","GET","application/json");
        try {requireSuccess(c); try (InputStream in=c.getInputStream()) {return readAll(in);}}
        finally {c.disconnect();}
    }
}
