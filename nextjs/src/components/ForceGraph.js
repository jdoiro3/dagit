"use client"; // This is a client component

import React, { useMemo, useRef, useState, useEffect } from "react";
import ForceGraph2D from "react-force-graph-2d";
import * as d3 from 'd3';
import ReactJson from '@microlink/react-json-view';
import ObjectModal from "./ObjectModal";
import useWebSocket, { ReadyState } from 'react-use-websocket';
import SyntaxHighlighter from 'react-syntax-highlighter';
import { docco } from 'react-syntax-highlighter/dist/esm/styles/hljs';

function extToLanguage(fileName) {
    const ext = fileName.slice(fileName.lastIndexOf("."));
    const extToLang = {
        ".js": "javascript",
        ".go": "go",
        ".py": "python",
        ".css": "css",
        ".json": "json",
    };
    if (ext in extToLang) {
        return extToLang[ext];
    } else {
        return "text";
    }
}

function processData(data) {
    let treeEntries = {};
    const gData = {
        nodes: data.nodes.map(n => {
            let value
            if (n.type === "blob") {
                value = n.object.content;
            } else {
                value = n;
            }
            let node = { id: n.name, type: n.type, value: value };
            if (node.type === "tree") {
                node.value.object.entries.forEach(e => treeEntries[e.hash] = e);
            }
            return node;
        }),
        links: data.edges.map(e => ({ source: e.src, target: e.dest }))
    };
    console.log(gData)
    return {gData: gData, treeEntries: treeEntries};
}

const ForceGraph = () => {
    const fgRef = useRef();

    const [graphData, setGraphData] = useState({ nodes: [], links: [] });
    const [treeEntries, setTreeEntries] = useState({})
    const [modalNode, setModalNode] = useState({});
    const [show, setShow] = useState(false);

    const { sendMessage, lastMessage, readyState } = useWebSocket("ws://localhost:8080/ws", {
        onOpen: () => {
            sendMessage("need-objects");
        },
        onMessage: (e) => {
            let data = JSON.parse(e.data);
            console.log(data);
            let {gData, treeEntries} = processData(data);
            console.log(gData)
            setGraphData(gData);
            setTreeEntries(treeEntries)
        },
        onClose: (e) => {
            console.log(e)
        },
        //Will attempt to reconnect on all close events, such as server shutting down
        shouldReconnect: (closeEvent) => true,
        heartbeat: {
            message: 'ping',
            returnMessage: 'pong',
            timeout: 30000, // 30 seconds, if no response is received, the connection will be closed
            interval: 5000, // every 5 seconds, a ping message will be sent
          },
    });

    const handleClose = () => {
        setShow(false);
        fgRef.current.resumeAnimation();
    };
    const handleShow = () => {
        fgRef.current.pauseAnimation();
        setShow(true);
    };

    useEffect(() => {
        const fg = fgRef.current;
        fg.d3Force('y', 
            d3.forceY()
                .y(node => {
                    if (node.type === "commit") {
                        return -100
                    } else if (node.type === "tree") {
                        return 0
                    } else {
                        return 800
                    }
                }).strength(.5)
        );
        fg.d3Force('collide', d3.forceCollide(30));
    }, []);

    return (
    <div>
        <ForceGraph2D
            ref={fgRef}
            graphData={graphData}
            linkDirectionalArrowLength={5}
            linkDirectionalArrowRelPos={1}
            nodeRelSize={10}
            linkOpacity={.7}
            nodeAutoColorBy={(n) => n.type}
            nodeLabel={n => {
                let style = "background-color:white; color:black; border-radius: 6px; padding:5px;";
                return `<div style="'${style}'">Type: '${n.type}'<br>objectname: '${n.id}'</div>`;
            }}
            onNodeDragEnd={node => {
                node.fx = node.x;
                node.fy = node.y;
            }}
            onNodeClick={node => {
                handleShow(true)
                setModalNode(node)
            }}
        />
        <ObjectModal 
            show={show} 
            handleClose={handleClose} 
            name={modalNode.id}
            content={
                modalNode.type !== "blob" ? 
                <ReactJson src={modalNode.value} name={null} />: 
                <SyntaxHighlighter language={extToLanguage(treeEntries[modalNode.id].name)} style={docco}>
                    {modalNode.value}
                </SyntaxHighlighter>
            }
        >
        </ObjectModal>
    </div>
    );
  };
  
  export default ForceGraph;