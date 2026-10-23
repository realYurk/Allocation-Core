import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
// Java 8+ integration sample, no Maven dependencies.
// Compile: javac -encoding UTF-8 AllocationCLIClient.java
// Usage: java AllocationCLIClient ./alloc_linux_amd64 RULE.json OUTPUT_DIR [workers]
// Invokes the standalone Go engine: this is not a native Java allocation engine.
public final class AllocationCLIClient {
    public static void main(String[] args) throws Exception {
        if (args.length!=3 && args.length!=4) {
            throw new IllegalArgumentException("Usage: executable config.json output_dir [workers]");
        }
        List<String> cmd = new ArrayList<String>();
        cmd.add(args[0]); cmd.add("run"); cmd.add(args[1]); cmd.add(args[2]);
        if (args.length==4) { cmd.add("--workers"); cmd.add(args[3]); }
        Process process = new ProcessBuilder(cmd).redirectErrorStream(true).start();
        try (BufferedReader r=new BufferedReader(new InputStreamReader(process.getInputStream(),StandardCharsets.UTF_8))) {
            String line; while((line=r.readLine())!=null) System.out.println(line);
        }
        int exit=process.waitFor();
        if (exit!=0) throw new IllegalStateException("Allocation failed: exit="+exit);
        Path out=Paths.get(args[2]);
        for (String name : Arrays.asList("results.csv","ledger.csv","reconciliation.csv","source_to_pool.csv","run_summary.json")) {
            if (!Files.isRegularFile(out.resolve(name))) throw new IOException("missing "+name);
        }
        String summary=new String(Files.readAllBytes(out.resolve("run_summary.json")),StandardCharsets.UTF_8);
        if (!summary.contains("\"status\": \"PASS\"")) throw new IOException("run_summary not PASS");
        System.out.println("Engine PASS. Validate pool reconciliation and data contract before external writeback: "+out);
    }
}
