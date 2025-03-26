"use client"; // This is a client component

import React, { useCallback, useRef, useState, useEffect } from "react";
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
    return ext in extToLang ? extToLang[ext] : "text";
}

function randomIntFromInterval(min, max) { // min and max included 
    return Math.floor(Math.random() * (max - min + 1) + min);
}

function toObj(arr, keyFunc) {
    var rv = {};
    for (var i = 0; i < arr.length; ++i)
      rv[keyFunc(arr[i])] = arr[i];
    return rv;
  }

function processGitData(data, currNodes) {
    let treeEntries = {};
    const gData = {
        nodes: data.nodes.map(obj => {
            let value = obj.type === "blob" ? obj.object.content: obj
            let node = { id: obj.name, type: obj.type, value: value };
            if (node.id in currNodes) {
                node = {...currNodes[node.id], ...node}
            }
            if (node.type === "tree") {
                node.value.object.entries.forEach(e => treeEntries[e.hash] = e);
            }
            return node;
        }),
        links: data.edges.map(e => ({ source: e.src, target: e.dest }))
    };
    return {gData: gData, treeEntries: treeEntries};
}

function setLinkData(gData) {
    gData.links.forEach(link => {
        const a = gData.nodes.find(obj => {
            return obj.id === link.source;
        });
        const b = gData.nodes.find(obj => {
            return obj.id === link.target;
        });
        !a.links && (a.links = []);
        !b.links && (b.links = []);
        a.links.push(link);
        b.links.push(link);
    });
}

function getCommitXAxis(gData) {
    let m = {};
    gData.nodes.filter((node) => node.type === "commit").sort((a, b) => {
        let aCommitTime = Date.parse(a.value.object.commitTime);
        let bCommitTime = Date.parse(b.value.object.commitTime);
        if (aCommitTime < bCommitTime) {
            return -1
        } else if (aCommitTime > bCommitTime) {
            return 1
        } else {
            return 0
        }
    }).forEach((node, i) => {
        m[node.value.object.commitTime] = (i*50)-500;
    })
    return m;
}

const ForceGraph = () => {
    const NODE_R = 20;
    const xMin = -500;
    const fgRef = useRef();

    const [graphData, setGraphData] = useState({ nodes: [], links: [] });
    const [treeEntries, setTreeEntries] = useState({})
    const [modalNode, setModalNode] = useState({});
    const [show, setShow] = useState(false);
    // for link highlighting
    const [highlightLinks, setHighlightLinks] = useState(new Set());
    const [commitDatesToX, setCommitDatesToX] = useState({})

    // handle messages from dagit server
    const { sendMessage, lastMessage, readyState } = useWebSocket("ws://localhost:8080/ws", {
        onOpen: () => {
            sendMessage("need-objects");
        },
        onMessage: (e) => {
            let data = JSON.parse(e.data);
            let {gData, treeEntries} = processGitData(data, toObj(graphData.nodes, n => n.id));
            setLinkData(gData);
            setCommitDatesToX(getCommitXAxis(gData));
            setGraphData(gData);
            setTreeEntries(treeEntries);
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

    const drawNode = useCallback((node, ctx, globalScale) => {
        if (node.type === "ref") {
            const label = node.id;
            const fontSize = Math.min(30, 15 / globalScale);
            ctx.font = `${fontSize}px Sans-Serif`;
            const textWidth = ctx.measureText(label).width;
            const bckgDimensions = [textWidth, fontSize].map(n => n + fontSize * 0.2); // some padding

            ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
            ctx.fillRect(node.x - bckgDimensions[0] / 2, node.y - bckgDimensions[1] / 2, ...bckgDimensions);

            ctx.textAlign = 'center';
            ctx.textBaseline = 'middle';
            ctx.fillStyle = node.color;
            ctx.fillText(label, node.x, node.y);

            node.__bckgDimensions = bckgDimensions; // to re-use in nodePointerAreaPaint
        } else {
            ctx.fillStyle = node.color;
            ctx.beginPath();
            ctx.arc(node.x, node.y, Math.min(NODE_R, 15 / globalScale), 0, 2 * Math.PI, false); 
            ctx.fill();
        }
    }, []);

    const handleClose = () => {
        setShow(false);
        fgRef.current.resumeAnimation();
    };

    const handleShow = () => {
        fgRef.current.pauseAnimation();
        setShow(true);
    };

    const handleNodeHover = node => {
        highlightLinks.clear();
        if (node) {
            node.links.forEach(link => highlightLinks.add(link));
        }
        setHighlightLinks(highlightLinks);
    };


    useEffect(() => {
        const xMax = Math.max(...Object.values(commitDatesToX));
        const fg = fgRef.current;
        fg.d3Force("y", 
            d3.forceY()
                .y(node => {
                    switch (node.type) {
                        case "ref":
                            return randomIntFromInterval(-600, -500);
                        case "commit":
                            return randomIntFromInterval(-150, -50);
                        case "tree":
                            const parentTree = graphData.nodes.find(n => n.type === "tree" && n.value.object.entries.find(e => e.hash === node.id));
                            if (parentTree) {
                                return randomIntFromInterval(80+300, 250+300);
                            } else {
                                return randomIntFromInterval(80, 250);
                            }
                        default:
                            return randomIntFromInterval(800, 1200);
                    }
                }).strength(.5)
        );
        fg.d3Force("x", 
            d3.forceX()
                .x(node => {
                    if (node.type === "commit") {
                        return commitDatesToX[node.value.object.commitTime];
                    } else if (node.type === "tree") {
                        const commit = graphData.nodes.find(n => n.type === "commit" && n.value.object.tree === node.id);
                        if (commit) {
                            return commitDatesToX[commit.value.object.commitTime];
                        } else {
                            return randomIntFromInterval(xMin, xMax);
                        }
                    } else if (node.type === "ref") {
                        const commit = graphData.nodes.find(n => n.id === node.value.object.commit);
                        if (commit) {
                            return commitDatesToX[commit.value.object.commitTime];
                        } else {
                            return randomIntFromInterval(xMin, xMax);
                        }
                    } else {
                        return randomIntFromInterval(xMin, xMax);
                    }
                }).strength(1)
        );
        fg.d3Force("link", d3.forceLink().strength(.001));
        fg.d3Force("collide", d3.forceCollide(30).strength(.5));
    }, [commitDatesToX, graphData.nodes]);

    return (
    <div>
        <ForceGraph2D
            ref={fgRef}
            graphData={graphData}
            linkDirectionalArrowLength={5}
            linkDirectionalArrowRelPos={1}
            linkDirectionalParticles={4}
            linkDirectionalParticleWidth={link => highlightLinks.has(link) ? 8 : 0}
            linkColor={link => highlightLinks.has(link) ? 'rgb(246, 164, 87)' : link.color}
            linkOpacity={link => highlightLinks.has(link) ? .8 : .01}
            nodeAutoColorBy={(n) => n.type}
            nodeLabel={n => {
                let style = "background-color:white; color:black; border-radius: 6px; padding:5px;";
                return `<div style="'${style}'">Type: '${n.type}'<br>objectname: '${n.id}'</div>`;
            }}
            onNodeHover={handleNodeHover}
            onNodeClick={node => {
                handleShow(true)
                setModalNode(node)
            }}
            onNodeDrag={node => {
                graphData.nodes.forEach(n => {
                    if (n.id !== node.id) {
                        n.fx = n.x;
                        n.fy = n.y;
                    } else {
                        console.log(node.x, node.y)
                    }
                })
            }}
            onNodeDragEnd={node => {
                graphData.nodes.forEach(n => {
                    if (n.id !== node.id) {
                        n.fx = null;
                        n.fy = null;
                    } else {
                        n.fx = node.x;
                        n.fy = node.y;
                    }
                })
            }}
            nodeCanvasObject={drawNode}
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