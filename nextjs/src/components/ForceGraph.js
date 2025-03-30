"use client"; // This is a client component

import React, { useCallback, useRef, useState, useEffect } from "react";
import ForceGraph2D from "react-force-graph-2d";
import * as d3 from 'd3';
import ReactJson from '@microlink/react-json-view';
import ObjectModal from "./ObjectModal";
import useWebSocket, { ReadyState } from 'react-use-websocket';
import SyntaxHighlighter from 'react-syntax-highlighter';
import { docco } from 'react-syntax-highlighter/dist/esm/styles/hljs';

const extToLanguage = (fileName) => {
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

const randomIntFromInterval = (min, max) => {
    return Math.floor(Math.random() * (max - min + 1) + min);
}

const convertMiliseconds = (miliseconds, format) => {
    var days, hours, minutes, seconds, total_hours, total_minutes, total_seconds;
    
    total_seconds = parseInt(Math.floor(miliseconds / 1000));
    total_minutes = parseInt(Math.floor(total_seconds / 60));
    total_hours = parseInt(Math.floor(total_minutes / 60));
    days = parseInt(Math.floor(total_hours / 24));
  
    seconds = parseInt(total_seconds % 60);
    minutes = parseInt(total_minutes % 60);
    hours = parseInt(total_hours % 24);
    
    switch(format) {
      case 's':
          return total_seconds;
      case 'm':
          return total_minutes;
      case 'h':
          return total_hours;
      case 'd':
          return days;
      default:
          return { d: days, h: hours, m: minutes, s: seconds };
    }
}

const toObj = (arr, keyFunc) => {
    var rv = {};
    for (var i = 0; i < arr.length; ++i)
      rv[keyFunc(arr[i])] = arr[i];
    return rv;
}

const processGitData = (data, currNodes) => {
    let treeEntries = {};
    const gData = {
        nodes: data.nodes.map(obj => {
            let value = obj.type === "blob" ? obj.object.content: obj;
            let node = { id: obj.name, type: obj.type, value: value, hidden: {} };
            switch (obj.type) {
                case "tree":
                    node.hidden = { commit: obj.commit };
                    delete node.value.commit;
                    break
                case "blob":
                    node.hidden = { firstCommitRef: node.firstCommitRef };
                    break
            }
            if (node.id in currNodes) {
                node = {...currNodes[node.id], ...node};
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

const setLinkData = (gData) => {
    gData.links.forEach(link => {
        const a = gData.nodes.find(obj => {
            return obj.id === link.source;
        });
        const b = gData.nodes.find(obj => {
            return obj.id === link.target;
        });
        !a.neighbors && (a.neighbors = []);
        !b.neighbors && (b.neighbors = []);
        a.neighbors.push(b);
        b.neighbors.push(a);

        if (a) {
            !a.links && (a.links = []);
            a.links.push(link);
            !a.neighbors && (a.neighbors = []);
            if (b) {
                a.neighbors.push(b);
            }
        }
        if (b) {
            !b.links && (b.links = []);
            b.links.push(link);
            !b.neighbors && (b.neighbors = []);
            if (a) {
                b.neighbors.push(a);
            }
        }
    })
}

const getCommitXAxis = (gData, xMin) => {
    let m = {};
    const commits = gData.nodes.filter((node) => node.type === "commit").sort((a, b) => {
        let aCommitTime = Date.parse(a.value.object.commitTime);
        let bCommitTime = Date.parse(b.value.object.commitTime);
        if (aCommitTime < bCommitTime) {
            return -1
        } else if (aCommitTime > bCommitTime) {
            return 1
        } else {
            return 0
        }
    });
    let currX = xMin;
    commits.forEach((c, i) => {
        const commitTime = Date.parse(c.value.object.commitTime);
        const prevCommitTime = i > 0 ? Date.parse(commits[i-1].value.object.commitTime) : 0;
        const xDiff = i === 0 ? 0 : Math.min(
            Math.max(convertMiliseconds(Math.abs(commitTime - prevCommitTime), "d"), 100),
            1000
        );
        currX = currX + xDiff;
        m[c.value.object.commitTime] = currX;
    });
    return m;
}

const getRandX = (node, min, max) => {
    if (node.randX && node.minX === min && node.maxX === max) {
        return node.randX;
    } else {
        node.randX = randomIntFromInterval(min, max);
        node.maxX = max;
        node.minX = min;
        return node.randX;
    }
}

const getRandY = (node, min, max) => {
    if (node.randY && node.minY === min && node.maxY === max) {
        return node.randY;
    } else {
        node.randY = randomIntFromInterval(min, max);
        node.minY = min;
        node.maxY = max;
        return node.randY;
    }
}

const ForceGraph = () => {
    const minNodeR = 5;
    const xMin = 0;
    const fgRef = useRef();

    const [graphData, setGraphData] = useState({ nodes: [], links: [] });
    const [treeEntries, setTreeEntries] = useState({})
    const [modalNode, setModalNode] = useState({});
    const [show, setShow] = useState(false);
    // for node and link highlighting
    const [highlightNodes, setHighlightNodes] = useState(new Set());
    const [highlightLinks, setHighlightLinks] = useState(new Set());
    const [hoverNode, setHoverNode] = useState(null);
    // used for setting the node's x axis position
    const [commitDatesToX, setCommitDatesToX] = useState({});

    // handle messages from dagit server
    const { sendMessage, lastMessage, readyState } = useWebSocket("ws://localhost:8080/ws", {
        onOpen: () => {
            sendMessage("need-objects");
        },
        onMessage: (e) => {
            let data = JSON.parse(e.data);
            let {gData, treeEntries} = processGitData(data, toObj(graphData.nodes, n => n.id));
            setLinkData(gData);
            setCommitDatesToX(getCommitXAxis(gData, xMin));
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

    const drawNode = (node, ctx, globalScale, assignFillStyle) => {
        if (node.type === "ref") {
            const label = node.id;
            const fontSize = Math.max(minNodeR, 15 / globalScale);
            ctx.font = `${fontSize}px Sans-Serif`;
            const textWidth = ctx.measureText(label).width;
            const bckgDimensions = [textWidth, fontSize].map(n => n + fontSize * 0.2); // some padding
    
            if (assignFillStyle) {
                ctx.fillStyle = 'rgba(255, 255, 255, 0.8)';
            }
            ctx.fillRect(node.x - bckgDimensions[0] / 2, node.y - bckgDimensions[1] / 2, ...bckgDimensions);
    
            ctx.textAlign = 'center';
            ctx.textBaseline = 'middle';
            ctx.fillStyle = node.color;
            ctx.fillText(label, node.x, node.y);
            node.radius = fontSize
        } else {
            const r = Math.max(minNodeR, Math.min(15 / globalScale, 30));
            node.radius = r;
            if (assignFillStyle) {
                if (hoverNode === null) {
                    ctx.fillStyle = node.color;
                } else {
                    ctx.fillStyle = highlightNodes.has(node) || node === hoverNode ? "orange" : node.color;
                }
            }
            ctx.beginPath();
            ctx.arc(node.x, node.y, r, 0, 2 * Math.PI, false); 
            ctx.fill();
        }
    }

    const handleClose = () => {
        setShow(false);
        fgRef.current.resumeAnimation();
    };

    const handleShow = () => {
        fgRef.current.pauseAnimation();
        setShow(true);
    };

    const updateHighlight = () => {
        setHighlightNodes(highlightNodes);
        setHighlightLinks(highlightLinks);
    };

    const handleNodeHover = node => {
        highlightNodes.clear();
        highlightLinks.clear();
        if (node && node.links) {
            highlightNodes.add(node);
            node.neighbors.forEach(neighbor => highlightNodes.add(neighbor));
            node.links.forEach(link => highlightLinks.add(link));
        }
        setHoverNode(node || null);
        updateHighlight();
    };

    useEffect(() => {
        let xMax = Math.max(...Object.values(commitDatesToX));
        if (!Number.isFinite(xMax)) {
            xMax = 500;
        }
        const fg = fgRef.current;
        fg.d3Force("center", null);
        fg.d3Force("y", 
            d3.forceY()
                .y(node => {
                    switch (node.type) {
                        case "ref":
                            return getRandY(node, -600, -500);
                        case "commit":
                            return getRandY(node, -350, -50);
                        case "tree":
                            if (node.hidden.commit) {
                                return getRandY(node, 80, 250);
                            } else {
                                return getRandY(node, 80+300, 250+300);
                            }
                        default:
                            return getRandY(node, 800, 1200);
                    }
                }).strength(.5)
        );
        fg.d3Force("x", 
            d3.forceX()
                .x(node => {
                    switch (node.type) {
                        case "commit":
                            return commitDatesToX[node.value.object.commitTime];
                        case "tree":
                            const treeCommit = graphData.nodes.find(n => n.type === "commit" && n.id === node.hidden.commit);
                            if (treeCommit) {
                                return commitDatesToX[treeCommit.value.object.commitTime]
                            } else {
                                return getRandX(node, xMin, xMax);
                            }
                        case "blob":
                            const blobCommit = graphData.nodes.find(n => n.type === "commit" && n.id === node.hidden.firstCommitRef);
                            if (blobCommit) {
                                return commitDatesToX[blobCommit.value.object.commitTime];
                            } else {
                                return getRandX(node, xMin, xMax);
                            }
                        case "ref":
                            const refCommit = graphData.nodes.find(n => {
                                return n.type === "commit" && n.id === node.value.object.commit
                            });
                            if (refCommit) {
                                return commitDatesToX[refCommit.value.object.commitTime];
                            } else {
                                return getRandX(node, xMin, xMax);
                            }
                        default:
                            return getRandX(node, xMin, xMax);
                    }
                }).strength(1)
        );
        fg.d3Force("link", d3.forceLink().strength(.001));
        fg.d3Force("collide", d3.forceCollide(10));
    }, [commitDatesToX, graphData.nodes, xMin]);

    return (
    <div>
        <ForceGraph2D
            ref={fgRef}
            graphData={graphData}
            linkDirectionalArrowLength={10}
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
            onNodeDragEnd={node => {
                node.fx = node.x;
                node.fy = node.y
            }}
            nodeCanvasObject={(node, ctx, globalScale) => drawNode(node, ctx, globalScale, true)}
            onZoomEnd={(transform) => {
                if (fgRef.current) {
                    fgRef.current.d3Force("collide", d3.forceCollide((node) => node.radius + 10));
                }
            }}
            nodePointerAreaPaint={(node, color, ctx, globalScale) => {
                ctx.fillStyle = color;
                drawNode(node, ctx, globalScale, false);
            }}
        />
        <ObjectModal 
            show={show} 
            handleClose={handleClose} 
            name={modalNode.id}
            content={
                modalNode.type !== "blob" ? 
                <ReactJson src={modalNode.value} name={null} />: 
                <SyntaxHighlighter language={treeEntries[modalNode.id] ? extToLanguage(treeEntries[modalNode.id].name) : "text"} style={docco}>
                    {modalNode.value}
                </SyntaxHighlighter>
            }
        >
        </ObjectModal>
    </div>
    );
  };
  
  export default ForceGraph;