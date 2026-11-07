package allocationcore;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;

/** Process client for Allocation Core. No financial calculation runs in Java. */
public final class Client {
    private final Path executable;
    private final long timeoutMillis;
    private static final int MAX_CAPTURE_BYTES = 4 * 1024 * 1024;

    public Client(Path executable, long timeoutMillis) throws IOException {
        if (executable == null || timeoutMillis <= 0) throw new IllegalArgumentException("executable and positive timeout required");
        this.executable = executable.toRealPath();
        this.timeoutMillis = timeoutMillis;
    }
    public static final class CoreException extends IOException {
        public final int exitCode;
        /** Raw JSON report when available, e.g. failed input inspection. */
        public final String reportJson;
        CoreException(String message, int exitCode, String reportJson) { super(message); this.exitCode=exitCode; this.reportJson=reportJson; }
    }
    public static final class Options {
        public int workers = 1;
        /** Zero selects memory; 2..64 selects disk. */
        public int diskPartitions = 0;
        public boolean driverDiskIndex = false;
        public Path driverCacheDir;
        public Path checkpointDir;
        public boolean resume = false;
        public boolean evidenceGzip = false;
    }
    public static final class CompareOptions {
        public int precision = 2;
        public String tolerance = "0";
        public boolean rejectReferenceDuplicates = false;
        public boolean rejectActualDuplicates = false;
    }
    private static String path(Path value) {
        if (value == null) throw new IllegalArgumentException("path required");
        return value.toAbsolutePath().normalize().toString();
    }
    private static final class Collector implements Runnable {
        private final InputStream stream;
        private final ByteArrayOutputStream output = new ByteArrayOutputStream();
        private boolean truncated;
        private IOException failure;
        Collector(InputStream stream) { this.stream=stream; }
        public void run() {
            byte[] buf = new byte[8192];
            try (InputStream input=stream) {
                int n;
                while ((n=input.read(buf))!=-1) {
                    int room=MAX_CAPTURE_BYTES-output.size();
                    if (room>0) output.write(buf,0,Math.min(n,room));
                    if (n>room) truncated=true;
                }
            } catch (IOException e) { failure=e; }
        }
        String text() { return new String(output.toByteArray(), StandardCharsets.UTF_8); }
    }
    private String invoke(List<String> args, boolean allowDifferences) throws IOException, InterruptedException {
        List<String> command=new ArrayList<>();command.add(executable.toString());command.addAll(args);
        Process process=new ProcessBuilder(command).start();
        process.getOutputStream().close();
        Collector out=new Collector(process.getInputStream()),err=new Collector(process.getErrorStream());
        Thread outThread=new Thread(out,"allocation-stdout"),errThread=new Thread(err,"allocation-stderr");
        outThread.setDaemon(true);errThread.setDaemon(true);outThread.start();errThread.start();
        try {
            if (!process.waitFor(timeoutMillis, TimeUnit.MILLISECONDS)) {
                process.destroyForcibly();process.waitFor();
                throw new CoreException("Core timed out; inspect temporary files before retrying",-1,"");
            }
            outThread.join();errThread.join();
            if (out.failure!=null) throw out.failure;
            if (err.failure!=null) throw err.failure;
            if (out.truncated || err.truncated) throw new CoreException("Core output exceeded capture limit",process.exitValue(),"");
            int exit=process.exitValue();String json=out.text();
            if (exit!=0 && !(allowDifferences && exit==3)) throw new CoreException(err.text().trim(),exit,json);
            return json; // Client application may parse JSON with its existing library.
        } finally { if(process.isAlive()) process.destroyForcibly(); }
    }
    public String inspect(Path rule) throws IOException, InterruptedException {
        List<String> args=new ArrayList<>();args.add("core");args.add("inspect");args.add(path(rule));args.add("--json");return invoke(args,false);
    }
    public String run(Path rule, Path output, Options options) throws IOException, InterruptedException {
        if (options==null) options=new Options();
        if (options.workers<1||options.workers>128) throw new IllegalArgumentException("workers must be 1..128");
        if (options.diskPartitions!=0&&(options.diskPartitions<2||options.diskPartitions>64)) throw new IllegalArgumentException("disk partitions must be 2..64");
        if (options.diskPartitions==0&&(options.driverDiskIndex||options.driverCacheDir!=null||options.checkpointDir!=null)) throw new IllegalArgumentException("disk options require partitions");
        if (options.driverCacheDir!=null&&!options.driverDiskIndex) throw new IllegalArgumentException("driver cache requires disk index");
        if (options.resume&&options.checkpointDir==null) throw new IllegalArgumentException("resume requires checkpoint directory");
        List<String> args=new ArrayList<>();args.add("core");args.add("run");args.add(path(rule));args.add(path(output));args.add("--workers");args.add(Integer.toString(options.workers));
        if(options.diskPartitions!=0){args.add("--disk-partitions");args.add(Integer.toString(options.diskPartitions));}
        if(options.driverDiskIndex)args.add("--driver-disk-index");
        if(options.driverCacheDir!=null){args.add("--driver-cache-dir");args.add(path(options.driverCacheDir));}
        if(options.checkpointDir!=null){args.add("--checkpoint-dir");args.add(path(options.checkpointDir));}
        if(options.resume)args.add("--resume");if(options.evidenceGzip)args.add("--evidence-gzip");
        return invoke(args,false);
    }
    public String compare(Path actual,Path reference,Path output,int precision,boolean rejectReferenceDuplicates) throws IOException,InterruptedException {
        CompareOptions options=new CompareOptions();options.precision=precision;options.rejectReferenceDuplicates=rejectReferenceDuplicates;
        return compare(actual,reference,output,options);
    }
    public String compare(Path actual,Path reference,Path output,CompareOptions options) throws IOException,InterruptedException {
        if(options==null)options=new CompareOptions();
        if(options.precision<0||options.precision>6)throw new IllegalArgumentException("precision must be 0..6");
        if(options.tolerance==null||options.tolerance.isEmpty())throw new IllegalArgumentException("tolerance is required");
        List<String> args=new ArrayList<>();args.add("reconcile");args.add("--actual");args.add(path(actual));args.add("--reference");args.add(path(reference));args.add("--out");args.add(path(output));args.add("--precision");args.add(Integer.toString(options.precision));args.add("--tolerance");args.add(options.tolerance);
        if(options.rejectReferenceDuplicates)args.add("--reject-reference-duplicates");
        if(options.rejectActualDuplicates)args.add("--reject-actual-duplicates");
        return invoke(args,true); // DIFFERENCES returns JSON even though Core exits 3.
    }
}
