import org.allocation.integration.AllocationClient;
import java.nio.file.*;
public final class DownloadProbe {
    public static void main(String[] args) throws Exception {
        AllocationClient c = new AllocationClient(args[0], "");
        if (!c.health().contains("ok")) throw new Exception("health failure");
        String id = args[1]; Path root = Paths.get(args[2]);
        for (String file : new String[] {"results.csv","reconciliation.csv","run_summary.json",
                         "ledger.csv.gz","source_to_pool.csv.gz","evidence_stream_manifest.json",
                         "lineage_links.csv.gz"}) {
            try {
                c.download(id,file,root.resolve(file));
                if (Files.size(root.resolve(file)) == 0) throw new Exception("empty "+file);
                System.out.println("DOWNLOADED " + file + " bytes=" + Files.size(root.resolve(file)));
            } catch (java.io.IOException e) {
                // Optional zero-row lineage files may not exist depending on business case.
                if (file.equals("lineage_links.csv.gz") && e.getMessage().contains("404")) {
                    System.out.println("OPTIONAL NOT GENERATED " + file); continue;
                }
                throw e;
            }
        }
        boolean blocked=false;
        try { c.download(id,"../../secrets",root.resolve("blocked")); }
        catch (IllegalArgumentException expected) { blocked=true; }
        if (!blocked) throw new Exception("invalid filename accepted");
        System.out.println("PASS: java8 download and endpoint allowlist");
    }
}
