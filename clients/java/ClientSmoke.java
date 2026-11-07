import allocationcore.Client;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.Files;
import java.nio.charset.StandardCharsets;

/** Local synthetic-only integration smoke test. */
public final class ClientSmoke {
    public static void main(String[] args) throws Exception {
        if(args.length!=4)throw new IllegalArgumentException("EXE RULE OUTPUT_ROOT REFERENCE");
        Client c=new Client(Paths.get(args[0]),30000);
        Path rule=Paths.get(args[1]), root=Paths.get(args[2]);
        if(!c.inspect(rule).contains("\"status\": \"PASS\""))throw new AssertionError("inspection");
        Client.Options disk=new Client.Options();disk.workers=2;disk.diskPartitions=4;disk.driverDiskIndex=true;
        for(int i=0;i<2;i++) {
            Path out=root.resolve(i==0?"java-memory":"java-disk");
            String result=c.run(rule,out,i==0?null:disk);
            if(!result.contains("\"output_amount\": \"1200.00\""))throw new AssertionError(result);
            String compare=c.compare(out.resolve("results.csv"),Paths.get(args[3]),root.resolve("java-reference-"+i),2,true);
            if(!compare.contains("\"status\": \"MATCH\""))throw new AssertionError(compare);
        }
        // DIFFERENCES is a normal comparison outcome (CLI exit 3), not an I/O error.
        String ref=new String(Files.readAllBytes(Paths.get(args[3])),StandardCharsets.UTF_8);
        Path wrong=root.resolve("wrong-reference.csv");Files.write(wrong,ref.replace("100.00","101.00").getBytes(StandardCharsets.UTF_8));
        String different=c.compare(root.resolve("java-memory").resolve("results.csv"),wrong,root.resolve("java-different"),2,false);
        if(!different.contains("\"status\": \"DIFFERENCES\""))throw new AssertionError(different);
        System.out.println("JAVA_CLIENT_PASS");
    }
}
