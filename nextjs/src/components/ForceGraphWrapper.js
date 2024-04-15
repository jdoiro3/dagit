"use client"; // This is a client component

import dynamic from "next/dynamic";

const ForceGraph = dynamic(() => import("./ForceGraph"), {
  ssr: false
});

export default ForceGraph;