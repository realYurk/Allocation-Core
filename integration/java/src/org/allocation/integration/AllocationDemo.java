package org.allocation.integration;
public final class AllocationDemo {
    public static void main(String[] args) throws Exception {
        String base = args.length > 0 ? args[0] : "http://127.0.0.1:8787";
        String rule = args.length > 1 ? args[1] : "manufacturing/rule.json";
        String token = args.length > 2 ? args[2] : System.getenv("ALLOCATION_TOKEN");
        AllocationClient client=new AllocationClient(base,token);
        System.out.println("HEALTH="+client.health());
        System.out.println("SUBMITTED="+client.submit(rule));
        System.out.println("Note: parse returned job.id with your application's JSON library, then poll status(jobId) and download().");
    }
}
