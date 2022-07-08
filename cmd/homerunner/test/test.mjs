import { Homerunner } from "homerunner-client";

const run = async () => {
    const client = new Homerunner.Client();
    console.log("homerunner base url:", client.baseUrl);
    console.log("creating homeserver...");
    const blueprint = await client.create({
        base_image_uri: "complement-dendrite",
        blueprint_name: "one_to_one_room",
    });
    console.log(blueprint);
    // verify blueprint fields
    if (!blueprint.expires) {
        throw new Error("missing 'expires' key in response");
    }
    const hs1 = blueprint.homeservers["hs1"];
    if (!hs1) {
        throw new Error("missing hs1 in response");
    }
    const wantKeys = ["BaseURL", "FedBaseURL", "ContainerID", "AccessTokens", "DeviceIDs"];
    wantKeys.forEach((k) => {
        if (!hs1[k]) {
            throw new Error("hs1 missing key: " + k);
        }
    });

    console.log("destroying homeserver...");
    await client.destroy("one_to_one_room");
};

run().then(() => {
    process.exit(0);
}).catch((err) => {
    console.error("Tests failed:",err.message);
    process.exit(1);
})
