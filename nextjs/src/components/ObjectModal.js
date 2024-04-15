import Button from 'react-bootstrap/Button';
import Modal from 'react-bootstrap/Modal';

function ObjectModal({show, handleClose, name, content}) {

    console.log(content)

    return (
        <>
        <Modal show={show} onHide={handleClose} size={"lg"} centered={true}>
            <Modal.Header closeButton>
            <Modal.Title>{name}</Modal.Title>
            </Modal.Header>
            <Modal.Body>{content}</Modal.Body>
            <Modal.Footer>
            <Button variant="secondary" onClick={handleClose}>
                Close
            </Button>
            </Modal.Footer>
        </Modal>
        </>
    );
}

export default ObjectModal;