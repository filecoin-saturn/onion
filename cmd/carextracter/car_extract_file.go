package main

import (
	"bytes"
	"context"
	cid "github.com/ipfs/go-cid"
	"github.com/ipfs/go-unixfsnode/file"
	"github.com/ipld/go-car/v2/blockstore"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/storage/bsadapter"
	"io"
)

func extractRaw(carBytes []byte) (response []byte) {
	cr := bytes.NewReader(carBytes)
	bs, err := blockstore.NewReadOnly(cr, nil)
	if err != nil {
		panic(err)
	}
	roots, err := bs.Roots()
	if err != nil {
		panic(err)
	}

	bsa := &bsadapter.Adapter{Wrapped: bs}
	ls := cidlink.DefaultLinkSystem()
	ls.TrustedStorage = true
	ls.SetReadStorage(bsa)

	return extractRoot(&ls, roots[0])
}

func extractRoot(ls *ipld.LinkSystem, root cid.Cid) (response []byte) {
	if root.Prefix().Codec == cid.Raw {
		panic("extractRoot: raw blocks not supported")
	}

	pbn, err := ls.Load(ipld.LinkContext{}, cidlink.Link{Cid: root}, dagpb.Type.PBNode)
	if err != nil {
		panic(err)
	}
	pbnode := pbn.(dagpb.PBNode)

	node, err := file.NewUnixFSFile(context.Background(), pbnode, ls)
	if err != nil {
		panic(err)
	}
	nlr, err := node.AsLargeBytes()
	if err != nil {
		panic(err)
	}

	resp := bytes.NewBuffer(nil)

	_, err = io.Copy(resp, nlr)
	if err != nil {
		panic(err)
	}
	return resp.Bytes()
}
